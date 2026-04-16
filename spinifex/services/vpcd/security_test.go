package vpcd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecurity_CreatePortGroup(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	require.NoError(t, err)
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	evt := SGEvent{
		GroupId: "sg-abc123",
		VpcId:   "vpc-test1",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicCreateSG, data, 5_000_000_000)
	require.NoError(t, err)
	assertSuccess(t, resp, "create SG port group")

	// Verify port group was created in the mock
	mock.mu.Lock()
	pg, exists := mock.portGroups["sg_abc123"]
	mock.mu.Unlock()
	assert.True(t, exists, "port group sg_abc123 should exist")
	assert.NotNil(t, pg)

	// Verify default deny ACLs were created (2 deny ACLs at priority 900)
	// and that each has logging enabled per CMMC SC.L1-3.13.1.
	snapshots := snapshotACLs(t, mock, "sg_abc123")
	assert.Equal(t, 2, len(snapshots), "should have 2 default deny ACLs (ingress + egress)")

	byDirection := map[string]aclSnapshot{}
	for _, s := range snapshots {
		assert.Equal(t, "drop", s.action, "default ACLs must all be drop")
		assert.True(t, s.log, "default deny ACL must have log=true for boundary monitoring")
		assert.Equal(t, "info", s.severity, "default deny ACL must use info severity")
		byDirection[s.direction] = s
	}

	ingress, hasIngress := byDirection["to-lport"]
	egress, hasEgress := byDirection["from-lport"]
	require.True(t, hasIngress, "must have a to-lport (ingress) deny ACL")
	require.True(t, hasEgress, "must have a from-lport (egress) deny ACL")
	assert.Equal(t, "sg_abc123-deny-ingress", ingress.name, "ingress deny ACL must be named <pg>-deny-ingress for syslog correlation")
	assert.Equal(t, "sg_abc123-deny-egress", egress.name, "egress deny ACL must be named <pg>-deny-egress for syslog correlation")
}

func TestSecurity_DeletePortGroup(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	require.NoError(t, err)
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Create first
	createEvt := SGEvent{GroupId: "sg-del1", VpcId: "vpc-test2"}
	data, _ := json.Marshal(createEvt)
	resp, err := nc.Request(TopicCreateSG, data, 5_000_000_000)
	require.NoError(t, err)
	assertSuccess(t, resp, "create SG for delete test")

	// Verify it exists
	mock.mu.Lock()
	_, exists := mock.portGroups["sg_del1"]
	mock.mu.Unlock()
	assert.True(t, exists)

	// Delete
	delEvt := SGEvent{GroupId: "sg-del1", VpcId: "vpc-test2"}
	data, _ = json.Marshal(delEvt)
	resp, err = nc.Request(TopicDeleteSG, data, 5_000_000_000)
	require.NoError(t, err)
	assertSuccess(t, resp, "delete SG port group")

	// Verify removed
	mock.mu.Lock()
	_, exists = mock.portGroups["sg_del1"]
	mock.mu.Unlock()
	assert.False(t, exists, "port group sg_del1 should be deleted")
}

func TestSecurity_UpdateSGAddRules(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	require.NoError(t, err)
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Create SG first (no rules)
	createEvt := SGEvent{GroupId: "sg-upd1", VpcId: "vpc-test3"}
	data, _ := json.Marshal(createEvt)
	resp, err := nc.Request(TopicCreateSG, data, 5_000_000_000)
	require.NoError(t, err)
	assertSuccess(t, resp, "create SG for update test")

	// Update with ingress rules
	updateEvt := SGEvent{
		GroupId: "sg-upd1",
		VpcId:   "vpc-test3",
		IngressRules: []SGRuleForACL{
			{IpProtocol: "tcp", FromPort: 22, ToPort: 22, CidrIp: "10.0.0.0/8"},
			{IpProtocol: "tcp", FromPort: 443, ToPort: 443, CidrIp: "0.0.0.0/0"},
		},
	}
	data, _ = json.Marshal(updateEvt)
	resp, err = nc.Request(TopicUpdateSG, data, 5_000_000_000)
	require.NoError(t, err)
	assertSuccess(t, resp, "update SG with ingress rules")

	// Verify ACLs were created: 2 default deny + 2 ingress allow = 4
	snapshots := snapshotACLs(t, mock, "sg_upd1")
	assert.Equal(t, 4, len(snapshots), "should have 4 ACLs (2 deny + 2 ingress allow)")

	// Check that at least one match contains tcp.dst == 22. Also verify
	// logging policy: denies logged with name+severity, allows not logged
	// (CMMC SC.L1-3.13.1). The severity/name checks here guard against a
	// regression where handleUpdateSG re-adds denies without them.
	foundSSH := false
	foundHTTPS := false
	byDirection := map[string]aclSnapshot{}
	for _, s := range snapshots {
		switch s.action {
		case "drop":
			assert.True(t, s.log, "deny ACL must be logged")
			assert.Equal(t, "info", s.severity, "deny ACL must use info severity on update path")
			byDirection[s.direction] = s
		case "allow-related":
			assert.False(t, s.log, "allow ACL must not be logged (high volume, low signal)")
		}
		if containsAll(s.match, "tcp.dst == 22", "ip4.src == 10.0.0.0/8") {
			foundSSH = true
		}
		if containsAll(s.match, "tcp.dst == 443") {
			foundHTTPS = true
		}
	}
	assert.True(t, foundSSH, "should have SSH ACL with source CIDR")
	assert.True(t, foundHTTPS, "should have HTTPS ACL")

	ingress, hasIngress := byDirection["to-lport"]
	egress, hasEgress := byDirection["from-lport"]
	require.True(t, hasIngress, "update path must re-add to-lport deny ACL")
	require.True(t, hasEgress, "update path must re-add from-lport deny ACL")
	assert.Equal(t, "sg_upd1-deny-ingress", ingress.name, "update path must re-add ingress deny with correct name")
	assert.Equal(t, "sg_upd1-deny-egress", egress.name, "update path must re-add egress deny with correct name")
}

// aclSnapshot is a copy of the ACL fields tests assert on. Captured under the
// mock's lock so assertions can run after unlock.
type aclSnapshot struct {
	direction string
	action    string
	match     string
	log       bool
	severity  string
	name      string
}

// snapshotACLs copies the ACLs attached to a port group into a slice of
// aclSnapshots. Holds mock.mu for the shortest window possible; lets each
// test assert on whichever fields it cares about.
func snapshotACLs(t *testing.T, mock *MockOVNClient, pgName string) []aclSnapshot {
	t.Helper()
	mock.mu.Lock()
	defer mock.mu.Unlock()
	pg, ok := mock.portGroups[pgName]
	if !ok {
		return nil
	}
	out := make([]aclSnapshot, 0, len(pg.ACLs))
	for _, uuid := range pg.ACLs {
		a := mock.acls[uuid]
		if a == nil {
			continue
		}
		snap := aclSnapshot{
			direction: a.Direction,
			action:    a.Action,
			match:     a.Match,
			log:       a.Log,
		}
		if a.Severity != nil {
			snap.severity = *a.Severity
		}
		if a.Name != nil {
			snap.name = *a.Name
		}
		out = append(out, snap)
	}
	return out
}

// containsAll checks if s contains all substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
