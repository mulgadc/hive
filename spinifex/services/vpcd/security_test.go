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
	mock.mu.Lock()
	aclCount := len(pg.ACLs)
	mock.mu.Unlock()
	assert.Equal(t, 2, aclCount, "should have 2 default deny ACLs (ingress + egress)")
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
	mock.mu.Lock()
	pg := mock.portGroups["sg_upd1"]
	aclCount := len(pg.ACLs)

	// Collect match strings from the ACLs
	var matches []string
	for _, aclUUID := range pg.ACLs {
		acl := mock.acls[aclUUID]
		if acl != nil {
			matches = append(matches, acl.Match)
		}
	}
	mock.mu.Unlock()

	assert.Equal(t, 4, aclCount, "should have 4 ACLs (2 deny + 2 ingress allow)")

	// Check that at least one match contains tcp.dst == 22
	foundSSH := false
	foundHTTPS := false
	for _, m := range matches {
		if containsAll(m, "tcp.dst == 22", "ip4.src == 10.0.0.0/8") {
			foundSSH = true
		}
		if containsAll(m, "tcp.dst == 443") {
			foundHTTPS = true
		}
	}
	assert.True(t, foundSSH, "should have SSH ACL with source CIDR")
	assert.True(t, foundHTTPS, "should have HTTPS ACL")
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
