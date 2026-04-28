package handlers_elbv2

import (
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccountID = "123456789012"

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	_, nc, _ := testutil.StartTestJetStream(t)

	store, err := NewStore(nc)
	require.NoError(t, err)
	return store
}

func newTestLB(id, name string) *LoadBalancerRecord {
	return &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/" + name + "/" + id,
		LoadBalancerID:  id,
		DNSName:         name + "-" + id + ".us-east-1.elb.spinifex.local",
		Name:            name,
		Scheme:          SchemeInternal,
		Type:            LoadBalancerTypeApplication,
		State:           StateActive,
		VpcId:           "vpc-test123",
		SecurityGroups:  []string{"sg-111"},
		Subnets:         []string{"subnet-aaa"},
		IPAddressType:   IPAddressTypeIPv4,
		AccountID:       testAccountID,
		CreatedAt:       time.Now().UTC(),
	}
}

func newTestTG(id, name string) *TargetGroupRecord {
	return &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":targetgroup/" + name + "/" + id,
		TargetGroupID:  id,
		Name:           name,
		Protocol:       ProtocolHTTP,
		Port:           80,
		VpcId:          "vpc-test123",
		TargetType:     "instance",
		HealthCheck:    DefaultHealthCheck(),
		AccountID:      testAccountID,
		CreatedAt:      time.Now().UTC(),
	}
}

func newTestListener(id, lbArn string) *ListenerRecord {
	return &ListenerRecord{
		ListenerArn:     "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":listener/app/my-alb/lb123/" + id,
		ListenerID:      id,
		LoadBalancerArn: lbArn,
		Protocol:        ProtocolHTTP,
		Port:            80,
		DefaultActions: []ListenerAction{
			{Type: ActionTypeForward, TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":targetgroup/my-tg/tg123"},
		},
		AccountID: testAccountID,
		CreatedAt: time.Now().UTC(),
	}
}

// recordOps describes the Put/Get/Delete/List interface for one record type
// so the lifecycle tests can exercise LB / TG / Listener with the same logic.
type recordOps[R any] struct {
	put       func(*Store, *R) error
	getByID   func(*Store, string) (*R, error)
	delete    func(*Store, string) error
	list      func(*Store) ([]*R, error)
	idOf      func(*R) string
	new       func(id string) *R
	zeroValue *R
}

var lbOps = recordOps[LoadBalancerRecord]{
	put:     (*Store).PutLoadBalancer,
	getByID: (*Store).GetLoadBalancer,
	delete:  (*Store).DeleteLoadBalancer,
	list:    func(s *Store) ([]*LoadBalancerRecord, error) { return s.ListLoadBalancers() },
	idOf:    func(r *LoadBalancerRecord) string { return r.LoadBalancerID },
	new:     func(id string) *LoadBalancerRecord { return newTestLB(id, "lb-"+id) },
}

var tgOps = recordOps[TargetGroupRecord]{
	put:     (*Store).PutTargetGroup,
	getByID: (*Store).GetTargetGroup,
	delete:  (*Store).DeleteTargetGroup,
	list:    func(s *Store) ([]*TargetGroupRecord, error) { return s.ListTargetGroups() },
	idOf:    func(r *TargetGroupRecord) string { return r.TargetGroupID },
	new:     func(id string) *TargetGroupRecord { return newTestTG(id, "tg-"+id) },
}

var listenerOps = recordOps[ListenerRecord]{
	put:     (*Store).PutListener,
	getByID: (*Store).GetListener,
	delete:  (*Store).DeleteListener,
	list:    func(s *Store) ([]*ListenerRecord, error) { return s.ListListeners() },
	idOf:    func(r *ListenerRecord) string { return r.ListenerID },
	new: func(id string) *ListenerRecord {
		lbArn := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/test/lb1"
		return newTestListener(id, lbArn)
	},
}

func runLifecycleTest[R any](t *testing.T, ops recordOps[R]) {
	t.Run("put and get", func(t *testing.T) {
		store := setupTestStore(t)
		rec := ops.new("getput1")

		require.NoError(t, ops.put(store, rec))

		got, err := ops.getByID(store, "getput1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "getput1", ops.idOf(got))
	})

	t.Run("get not found", func(t *testing.T) {
		store := setupTestStore(t)
		got, err := ops.getByID(store, "nonexistent")
		require.NoError(t, err)
		assert.Equal(t, ops.zeroValue, got)
	})

	t.Run("delete removes record", func(t *testing.T) {
		store := setupTestStore(t)
		rec := ops.new("del1")
		require.NoError(t, ops.put(store, rec))
		require.NoError(t, ops.delete(store, "del1"))

		got, err := ops.getByID(store, "del1")
		require.NoError(t, err)
		assert.Equal(t, ops.zeroValue, got)
	})

	t.Run("delete idempotent", func(t *testing.T) {
		store := setupTestStore(t)
		require.NoError(t, ops.delete(store, "doesnt-exist"))
	})

	t.Run("list returns all", func(t *testing.T) {
		store := setupTestStore(t)
		require.NoError(t, ops.put(store, ops.new("a")))
		require.NoError(t, ops.put(store, ops.new("b")))

		records, err := ops.list(store)
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("list empty", func(t *testing.T) {
		store := setupTestStore(t)
		records, err := ops.list(store)
		require.NoError(t, err)
		assert.Empty(t, records)
	})
}

func TestLoadBalancerStoreLifecycle(t *testing.T) { runLifecycleTest(t, lbOps) }
func TestTargetGroupStoreLifecycle(t *testing.T)  { runLifecycleTest(t, tgOps) }
func TestListenerStoreLifecycle(t *testing.T)     { runLifecycleTest(t, listenerOps) }

// --- LB-specific lookups (no equivalent on TG/Listener) ---

func TestGetLoadBalancerByArn(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("arn123", "arn-test")
	require.NoError(t, store.PutLoadBalancer(lb))

	got, err := store.GetLoadBalancerByArn(lb.LoadBalancerArn)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, lb.Name, got.Name)

	got, err = store.GetLoadBalancerByArn("arn:nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetLoadBalancerByName(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("name123", "find-by-name")
	require.NoError(t, store.PutLoadBalancer(lb))

	got, err := store.GetLoadBalancerByName("find-by-name", testAccountID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, lb.LoadBalancerID, got.LoadBalancerID)
}

func TestPutLoadBalancer_Update(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("upd123", "updatable")
	require.NoError(t, store.PutLoadBalancer(lb))

	lb.State = StateFailed
	lb.ENIs = []string{"eni-111", "eni-222"}
	require.NoError(t, store.PutLoadBalancer(lb))

	got, err := store.GetLoadBalancer("upd123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, StateFailed, got.State)
	assert.Equal(t, []string{"eni-111", "eni-222"}, got.ENIs)
}

// --- TG-specific lookups + targets ---

func TestGetTargetGroupByArn(t *testing.T) {
	store := setupTestStore(t)
	tg := newTestTG("tgarn", "arn-tg")
	require.NoError(t, store.PutTargetGroup(tg))

	got, err := store.GetTargetGroupByArn(tg.TargetGroupArn)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, tg.Name, got.Name)
}

func TestGetTargetGroupByName(t *testing.T) {
	store := setupTestStore(t)
	tg := newTestTG("tgname", "named-tg")
	require.NoError(t, store.PutTargetGroup(tg))

	got, err := store.GetTargetGroupByName("named-tg", "vpc-test123")
	require.NoError(t, err)
	require.NotNil(t, got)

	// Wrong VPC should not find it
	got, err = store.GetTargetGroupByName("named-tg", "vpc-other")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestTargetGroupWithTargets(t *testing.T) {
	store := setupTestStore(t)
	tg := newTestTG("tgtargets", "targets-tg")
	tg.Targets = []Target{
		{Id: "i-aaa111", Port: 8080, HealthState: TargetHealthInitial, PrivateIP: "10.0.1.10"},
		{Id: "i-bbb222", Port: 0, HealthState: TargetHealthHealthy, PrivateIP: "10.0.1.11"},
	}
	require.NoError(t, store.PutTargetGroup(tg))

	got, err := store.GetTargetGroup("tgtargets")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Len(t, got.Targets, 2)
	assert.Equal(t, "i-aaa111", got.Targets[0].Id)
	assert.Equal(t, int64(8080), got.Targets[0].Port)
	assert.Equal(t, "10.0.1.11", got.Targets[1].PrivateIP)
}

// --- Listener-specific lookups ---

func TestListListenersByLB(t *testing.T) {
	store := setupTestStore(t)
	lbArn1 := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/alb1/lb1"
	lbArn2 := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/alb2/lb2"

	l1 := newTestListener("lst1", lbArn1)
	l1.Port = 80
	l2 := newTestListener("lst2", lbArn1)
	l2.Port = 443
	l3 := newTestListener("lst3", lbArn2)
	l3.Port = 80

	require.NoError(t, store.PutListener(l1))
	require.NoError(t, store.PutListener(l2))
	require.NoError(t, store.PutListener(l3))

	listeners, err := store.ListListenersByLB(lbArn1)
	require.NoError(t, err)
	assert.Len(t, listeners, 2)

	listeners, err = store.ListListenersByLB(lbArn2)
	require.NoError(t, err)
	assert.Len(t, listeners, 1)
}

func TestGetListenerByArn(t *testing.T) {
	store := setupTestStore(t)
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/test/lb1"
	l := newTestListener("lstarn", lbArn)
	require.NoError(t, store.PutListener(l))

	got, err := store.GetListenerByArn(l.ListenerArn)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, l.ListenerID, got.ListenerID)
}

// --- Cross-resource isolation: shared IDs across record types must not collide ---

func TestResourceIsolation(t *testing.T) {
	store := setupTestStore(t)

	lb := newTestLB("shared1", "alb-shared")
	tg := newTestTG("shared1", "tg-shared") // Same ID as LB
	l := newTestListener("shared1", lb.LoadBalancerArn)

	require.NoError(t, store.PutLoadBalancer(lb))
	require.NoError(t, store.PutTargetGroup(tg))
	require.NoError(t, store.PutListener(l))

	gotLB, err := store.GetLoadBalancer("shared1")
	require.NoError(t, err)
	require.NotNil(t, gotLB)
	assert.Equal(t, "alb-shared", gotLB.Name)

	gotTG, err := store.GetTargetGroup("shared1")
	require.NoError(t, err)
	require.NotNil(t, gotTG)
	assert.Equal(t, "tg-shared", gotTG.Name)

	gotL, err := store.GetListener("shared1")
	require.NoError(t, err)
	require.NotNil(t, gotL)
	assert.Equal(t, ProtocolHTTP, gotL.Protocol)

	// Deleting one type should not affect others
	require.NoError(t, store.DeleteLoadBalancer("shared1"))
	gotTG, _ = store.GetTargetGroup("shared1")
	assert.NotNil(t, gotTG)
	gotL, _ = store.GetListener("shared1")
	assert.NotNil(t, gotL)
}

func TestTargetGroupsForLB(t *testing.T) {
	store := setupTestStore(t)

	// Non-existent LB returns nil, nil
	tgs, err := store.TargetGroupsForLB("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, tgs)

	lb := newTestLB("tgflb1", "my-alb")
	tg := newTestTG("tg001", "my-tg")
	require.NoError(t, store.PutLoadBalancer(lb))
	require.NoError(t, store.PutTargetGroup(tg))

	listener := &ListenerRecord{
		ListenerArn:     "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":listener/app/my-alb/tgflb1/lst1",
		ListenerID:      "lst1",
		LoadBalancerArn: lb.LoadBalancerArn,
		Protocol:        ProtocolHTTP,
		Port:            80,
		DefaultActions: []ListenerAction{
			{Type: ActionTypeForward, TargetGroupArn: tg.TargetGroupArn},
			{Type: ActionTypeForward, TargetGroupArn: ""}, // empty ARN should be skipped
		},
		AccountID: testAccountID,
	}
	require.NoError(t, store.PutListener(listener))

	tgs, err = store.TargetGroupsForLB("tgflb1")
	require.NoError(t, err)
	require.Len(t, tgs, 1)
	assert.Equal(t, "tg001", tgs[0].TargetGroupID)
}
