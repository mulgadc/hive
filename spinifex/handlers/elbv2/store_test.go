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

// --- Load Balancer tests ---

func TestPutAndGetLoadBalancer(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("abc123", "my-alb")

	err := store.PutLoadBalancer(lb)
	require.NoError(t, err)

	got, err := store.GetLoadBalancer("abc123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, lb.Name, got.Name)
	assert.Equal(t, lb.LoadBalancerArn, got.LoadBalancerArn)
	assert.Equal(t, lb.VpcId, got.VpcId)
	assert.Equal(t, lb.Scheme, got.Scheme)
}

func TestGetLoadBalancer_NotFound(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetLoadBalancer("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestStoreDeleteLoadBalancer(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("del123", "delete-me")

	require.NoError(t, store.PutLoadBalancer(lb))
	require.NoError(t, store.DeleteLoadBalancer("del123"))

	got, err := store.GetLoadBalancer("del123")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDeleteLoadBalancer_Idempotent(t *testing.T) {
	store := setupTestStore(t)
	err := store.DeleteLoadBalancer("doesnt-exist")
	require.NoError(t, err)
}

func TestListLoadBalancers(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.PutLoadBalancer(newTestLB("lb1", "alb-one")))
	require.NoError(t, store.PutLoadBalancer(newTestLB("lb2", "alb-two")))

	lbs, err := store.ListLoadBalancers()
	require.NoError(t, err)
	assert.Len(t, lbs, 2)
}

func TestListLoadBalancers_Empty(t *testing.T) {
	store := setupTestStore(t)
	lbs, err := store.ListLoadBalancers()
	require.NoError(t, err)
	assert.Nil(t, lbs)
}

func TestGetLoadBalancerByArn(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("arn123", "arn-test")
	require.NoError(t, store.PutLoadBalancer(lb))

	got, err := store.GetLoadBalancerByArn(lb.LoadBalancerArn)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, lb.Name, got.Name)
}

func TestGetLoadBalancerByArn_NotFound(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetLoadBalancerByArn("arn:nonexistent")
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

// --- Target Group tests ---

func TestPutAndGetTargetGroup(t *testing.T) {
	store := setupTestStore(t)
	tg := newTestTG("tg123", "my-tg")

	require.NoError(t, store.PutTargetGroup(tg))

	got, err := store.GetTargetGroup("tg123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, tg.Name, got.Name)
	assert.Equal(t, tg.Protocol, got.Protocol)
	assert.Equal(t, tg.Port, got.Port)
	assert.Equal(t, tg.HealthCheck.Path, got.HealthCheck.Path)
}

func TestGetTargetGroup_NotFound(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetTargetGroup("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestStoreDeleteTargetGroup(t *testing.T) {
	store := setupTestStore(t)
	tg := newTestTG("tgdel", "delete-tg")

	require.NoError(t, store.PutTargetGroup(tg))
	require.NoError(t, store.DeleteTargetGroup("tgdel"))

	got, err := store.GetTargetGroup("tgdel")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestListTargetGroups(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.PutTargetGroup(newTestTG("tg1", "tg-one")))
	require.NoError(t, store.PutTargetGroup(newTestTG("tg2", "tg-two")))
	require.NoError(t, store.PutTargetGroup(newTestTG("tg3", "tg-three")))

	tgs, err := store.ListTargetGroups()
	require.NoError(t, err)
	assert.Len(t, tgs, 3)
}

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

// --- Listener tests ---

func TestPutAndGetListener(t *testing.T) {
	store := setupTestStore(t)
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/my-alb/lb123"
	l := newTestListener("lst123", lbArn)

	require.NoError(t, store.PutListener(l))

	got, err := store.GetListener("lst123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ProtocolHTTP, got.Protocol)
	assert.Equal(t, int64(80), got.Port)
	assert.Len(t, got.DefaultActions, 1)
	assert.Equal(t, ActionTypeForward, got.DefaultActions[0].Type)
}

func TestGetListener_NotFound(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetListener("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestStoreDeleteListener(t *testing.T) {
	store := setupTestStore(t)
	lbArn := "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/test/lb1"
	l := newTestListener("lstdel", lbArn)

	require.NoError(t, store.PutListener(l))
	require.NoError(t, store.DeleteListener("lstdel"))

	got, err := store.GetListener("lstdel")
	require.NoError(t, err)
	assert.Nil(t, got)
}

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

	// Should return only listeners for lbArn1
	listeners, err := store.ListListenersByLB(lbArn1)
	require.NoError(t, err)
	assert.Len(t, listeners, 2)

	// Should return only listener for lbArn2
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

// --- Cross-resource isolation tests ---

func TestResourceIsolation(t *testing.T) {
	// Verify that LB, TG, and Listener records don't interfere with each other
	store := setupTestStore(t)

	lb := newTestLB("shared1", "alb-shared")
	tg := newTestTG("shared1", "tg-shared") // Same ID as LB
	l := newTestListener("shared1", lb.LoadBalancerArn)

	require.NoError(t, store.PutLoadBalancer(lb))
	require.NoError(t, store.PutTargetGroup(tg))
	require.NoError(t, store.PutListener(l))

	// Each should be retrievable independently
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

	// List operations should return correct counts
	lbs, _ := store.ListLoadBalancers()
	assert.Len(t, lbs, 1)
	tgs, _ := store.ListTargetGroups()
	assert.Len(t, tgs, 1)
	listeners, _ := store.ListListeners()
	assert.Len(t, listeners, 1)

	// Deleting one type should not affect others
	require.NoError(t, store.DeleteLoadBalancer("shared1"))
	gotTG, _ = store.GetTargetGroup("shared1")
	assert.NotNil(t, gotTG)
	gotL, _ = store.GetListener("shared1")
	assert.NotNil(t, gotL)
}

func TestPutLoadBalancer_Update(t *testing.T) {
	store := setupTestStore(t)
	lb := newTestLB("upd123", "updatable")
	require.NoError(t, store.PutLoadBalancer(lb))

	// Update state
	lb.State = StateFailed
	lb.ENIs = []string{"eni-111", "eni-222"}
	require.NoError(t, store.PutLoadBalancer(lb))

	got, err := store.GetLoadBalancer("upd123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, StateFailed, got.State)
	assert.Equal(t, []string{"eni-111", "eni-222"}, got.ENIs)
}
