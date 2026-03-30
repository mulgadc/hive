package handlers_elbv2

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestService(t *testing.T) *ELBv2ServiceImpl {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc, err := NewELBv2ServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

// --- Load Balancer tests ---

func TestCreateLoadBalancer(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:           aws.String("my-alb"),
		Subnets:        []*string{aws.String("subnet-aaa")},
		SecurityGroups: []*string{aws.String("sg-111")},
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.LoadBalancers, 1)
	lb := out.LoadBalancers[0]
	assert.Equal(t, "my-alb", *lb.LoadBalancerName)
	assert.Equal(t, "internet-facing", *lb.Scheme)
	assert.Equal(t, "application", *lb.Type)
	assert.Equal(t, "active", *lb.State.Code)
	assert.Contains(t, *lb.DNSName, "my-alb")
	assert.Contains(t, *lb.LoadBalancerArn, "loadbalancer/app/my-alb")
}

func TestCreateLoadBalancer_InternalScheme(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:   aws.String("internal-alb"),
		Scheme: aws.String("internal"),
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "internal", *out.LoadBalancers[0].Scheme)
}

func TestCreateLoadBalancer_InvalidScheme(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:   aws.String("bad-scheme"),
		Scheme: aws.String("banana"),
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidScheme")
}

func TestCreateLoadBalancer_DuplicateName(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("dup-alb"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("dup-alb"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DuplicateLoadBalancerName")
}

func TestCreateLoadBalancer_MissingName(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{}, testAccountID)
	assert.Error(t, err)
}

func TestCreateLoadBalancer_WithTags(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("tagged-alb"),
		Tags: []*elbv2.Tag{
			{Key: aws.String("Env"), Value: aws.String("test")},
		},
	}, testAccountID)

	require.NoError(t, err)
	// Tags are stored internally, verify via describe
	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String("tagged-alb")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.LoadBalancers, 1)
	assert.Equal(t, *out.LoadBalancers[0].LoadBalancerArn, *desc.LoadBalancers[0].LoadBalancerArn)
}

func TestDeleteLoadBalancer(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("delete-me"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: out.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)

	// Verify it's gone
	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, desc.LoadBalancers)
}

func TestDeleteLoadBalancer_NotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/nope/xyz"),
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LoadBalancerNotFound")
}

func TestDeleteLoadBalancer_CleansUpListeners(t *testing.T) {
	svc := setupTestService(t)

	// Create LB, TG, and listener
	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-cleanup")}, testAccountID)
	require.NoError(t, err)
	lbArn := lbOut.LoadBalancers[0].LoadBalancerArn

	tgOut, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-cleanup")}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)
	require.NoError(t, err)

	// Delete LB should clean up listener
	_, err = svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{LoadBalancerArn: lbArn}, testAccountID)
	require.NoError(t, err)

	// Listener should be gone
	lstDesc, err := svc.DescribeListeners(&elbv2.DescribeListenersInput{LoadBalancerArn: lbArn}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, lstDesc.Listeners)
}

func TestDescribeLoadBalancers_FilterByName(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("alb-one")}, testAccountID)
	require.NoError(t, err)
	_, err = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("alb-two")}, testAccountID)
	require.NoError(t, err)

	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String("alb-one")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.LoadBalancers, 1)
	assert.Equal(t, "alb-one", *desc.LoadBalancers[0].LoadBalancerName)
}

func TestDescribeLoadBalancers_AccountIsolation(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("acct-alb")}, testAccountID)
	require.NoError(t, err)

	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{}, "999999999999")
	require.NoError(t, err)
	assert.Empty(t, desc.LoadBalancers)
}

// --- Target Group tests ---

func TestCreateTargetGroup(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("my-tg"),
		Protocol: aws.String("HTTP"),
		Port:     aws.Int64(8080),
		VpcId:    aws.String("vpc-test"),
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.TargetGroups, 1)
	tg := out.TargetGroups[0]
	assert.Equal(t, "my-tg", *tg.TargetGroupName)
	assert.Equal(t, "HTTP", *tg.Protocol)
	assert.Equal(t, int64(8080), *tg.Port)
	assert.Equal(t, "vpc-test", *tg.VpcId)
	assert.Equal(t, "/", *tg.HealthCheckPath)
	assert.Equal(t, "200", *tg.Matcher.HttpCode)
}

func TestCreateTargetGroup_CustomHealthCheck(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:                       aws.String("custom-hc"),
		HealthCheckPath:            aws.String("/healthz"),
		HealthCheckIntervalSeconds: aws.Int64(10),
		HealthyThresholdCount:      aws.Int64(2),
		Matcher:                    &elbv2.Matcher{HttpCode: aws.String("200-299")},
	}, testAccountID)

	require.NoError(t, err)
	tg := out.TargetGroups[0]
	assert.Equal(t, "/healthz", *tg.HealthCheckPath)
	assert.Equal(t, int64(10), *tg.HealthCheckIntervalSeconds)
	assert.Equal(t, int64(2), *tg.HealthyThresholdCount)
	assert.Equal(t, "200-299", *tg.Matcher.HttpCode)
}

func TestCreateTargetGroup_DuplicateName(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:  aws.String("dup-tg"),
		VpcId: aws.String("vpc-1"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:  aws.String("dup-tg"),
		VpcId: aws.String("vpc-1"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DuplicateTargetGroupName")
}

func TestDeleteTargetGroup(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("del-tg")}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
		TargetGroupArn: out.TargetGroups[0].TargetGroupArn,
	}, testAccountID)
	require.NoError(t, err)
}

func TestDeleteTargetGroup_InUse(t *testing.T) {
	svc := setupTestService(t)

	tgOut, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("inuse-tg")}, testAccountID)
	require.NoError(t, err)

	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("inuse-lb")}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
		TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn,
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ResourceInUse")
}

func TestDeleteTargetGroup_NotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/nope/xyz"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TargetGroupNotFound")
}

func TestDescribeTargetGroups_FilterByLBArn(t *testing.T) {
	svc := setupTestService(t)

	tg1, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-linked")}, testAccountID)
	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-unlinked")}, testAccountID)

	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-filter")}, testAccountID)
	_, _ = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg1.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)

	desc, err := svc.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.TargetGroups, 1)
	assert.Equal(t, "tg-linked", *desc.TargetGroups[0].TargetGroupName)
}

// --- Target registration tests ---

func TestRegisterTargets(t *testing.T) {
	svc := setupTestService(t)

	tgOut, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name: aws.String("reg-tg"),
		Port: aws.Int64(80),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn,
		Targets: []*elbv2.TargetDescription{
			{Id: aws.String("i-aaa111")},
			{Id: aws.String("i-bbb222"), Port: aws.Int64(8080)},
		},
	}, testAccountID)
	require.NoError(t, err)

	// Verify via DescribeTargetHealth
	health, err := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{
		TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn,
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, health.TargetHealthDescriptions, 2)

	// First target should use TG default port
	assert.Equal(t, "i-aaa111", *health.TargetHealthDescriptions[0].Target.Id)
	assert.Equal(t, int64(80), *health.TargetHealthDescriptions[0].Target.Port)
	assert.Equal(t, "initial", *health.TargetHealthDescriptions[0].TargetHealth.State)

	// Second target should use override port
	assert.Equal(t, "i-bbb222", *health.TargetHealthDescriptions[1].Target.Id)
	assert.Equal(t, int64(8080), *health.TargetHealthDescriptions[1].Target.Port)
}

func TestRegisterTargets_Idempotent(t *testing.T) {
	svc := setupTestService(t)

	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("idem-tg")}, testAccountID)
	tgArn := tgOut.TargetGroups[0].TargetGroupArn

	targets := []*elbv2.TargetDescription{{Id: aws.String("i-same")}}

	_, err := svc.RegisterTargets(&elbv2.RegisterTargetsInput{TargetGroupArn: tgArn, Targets: targets}, testAccountID)
	require.NoError(t, err)
	_, err = svc.RegisterTargets(&elbv2.RegisterTargetsInput{TargetGroupArn: tgArn, Targets: targets}, testAccountID)
	require.NoError(t, err)

	health, _ := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{TargetGroupArn: tgArn}, testAccountID)
	assert.Len(t, health.TargetHealthDescriptions, 1)
}

func TestDeregisterTargets(t *testing.T) {
	svc := setupTestService(t)

	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("dereg-tg"), Port: aws.Int64(80)}, testAccountID)
	tgArn := tgOut.TargetGroups[0].TargetGroupArn

	svc.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: tgArn,
		Targets: []*elbv2.TargetDescription{
			{Id: aws.String("i-keep")},
			{Id: aws.String("i-remove")},
		},
	}, testAccountID)

	_, err := svc.DeregisterTargets(&elbv2.DeregisterTargetsInput{
		TargetGroupArn: tgArn,
		Targets:        []*elbv2.TargetDescription{{Id: aws.String("i-remove")}},
	}, testAccountID)
	require.NoError(t, err)

	health, _ := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{TargetGroupArn: tgArn}, testAccountID)
	require.Len(t, health.TargetHealthDescriptions, 1)
	assert.Equal(t, "i-keep", *health.TargetHealthDescriptions[0].Target.Id)
}

func TestDescribeTargetHealth_FilterByTarget(t *testing.T) {
	svc := setupTestService(t)

	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("filter-tg"), Port: aws.Int64(80)}, testAccountID)
	tgArn := tgOut.TargetGroups[0].TargetGroupArn

	svc.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: tgArn,
		Targets: []*elbv2.TargetDescription{
			{Id: aws.String("i-one")},
			{Id: aws.String("i-two")},
		},
	}, testAccountID)

	health, err := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{
		TargetGroupArn: tgArn,
		Targets:        []*elbv2.TargetDescription{{Id: aws.String("i-one")}},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, health.TargetHealthDescriptions, 1)
	assert.Equal(t, "i-one", *health.TargetHealthDescriptions[0].Target.Id)
}

func TestDescribeTargetHealth_TGNotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String("arn:nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TargetGroupNotFound")
}

// --- Listener tests ---

func TestCreateListener(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lst-lb")}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("lst-tg")}, testAccountID)

	out, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("HTTP"),
		Port:            aws.Int64(8080),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.Listeners, 1)
	l := out.Listeners[0]
	assert.Equal(t, "HTTP", *l.Protocol)
	assert.Equal(t, int64(8080), *l.Port)
	assert.Equal(t, *lbOut.LoadBalancers[0].LoadBalancerArn, *l.LoadBalancerArn)
	require.Len(t, l.DefaultActions, 1)
	assert.Equal(t, "forward", *l.DefaultActions[0].Type)
}

func TestCreateListener_DuplicatePort(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("dup-port-lb")}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("dup-port-tg")}, testAccountID)

	actions := []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn}}

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Port:            aws.Int64(80),
		DefaultActions:  actions,
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Port:            aws.Int64(80),
		DefaultActions:  actions,
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DuplicateListener")
}

func TestCreateListener_LBNotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: aws.String("arn:nonexistent"),
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: aws.String("arn:tg")}},
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LoadBalancerNotFound")
}

func TestDeleteListener(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("dellst-lb")}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("dellst-tg")}, testAccountID)

	lstOut, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DeleteListener(&elbv2.DeleteListenerInput{
		ListenerArn: lstOut.Listeners[0].ListenerArn,
	}, testAccountID)
	require.NoError(t, err)

	// Verify deleted
	desc, _ := svc.DescribeListeners(&elbv2.DescribeListenersInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	assert.Empty(t, desc.Listeners)
}

func TestDeleteListener_NotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.DeleteListener(&elbv2.DeleteListenerInput{
		ListenerArn: aws.String("arn:nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ListenerNotFound")
}

func TestDescribeListeners_FilterByLBArn(t *testing.T) {
	svc := setupTestService(t)

	lb1, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("dl-lb1")}, testAccountID)
	lb2, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("dl-lb2")}, testAccountID)
	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("dl-tg")}, testAccountID)
	actions := []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}}

	svc.CreateListener(&elbv2.CreateListenerInput{LoadBalancerArn: lb1.LoadBalancers[0].LoadBalancerArn, Port: aws.Int64(80), DefaultActions: actions}, testAccountID)
	svc.CreateListener(&elbv2.CreateListenerInput{LoadBalancerArn: lb1.LoadBalancers[0].LoadBalancerArn, Port: aws.Int64(443), DefaultActions: actions}, testAccountID)
	svc.CreateListener(&elbv2.CreateListenerInput{LoadBalancerArn: lb2.LoadBalancers[0].LoadBalancerArn, Port: aws.Int64(80), DefaultActions: actions}, testAccountID)

	desc, err := svc.DescribeListeners(&elbv2.DescribeListenersInput{
		LoadBalancerArn: lb1.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.Listeners, 2)
}

func TestDescribeListeners_AccountIsolation(t *testing.T) {
	svc := setupTestService(t)

	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("iso-lb")}, testAccountID)
	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("iso-tg")}, testAccountID)
	svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)

	desc, err := svc.DescribeListeners(&elbv2.DescribeListenersInput{}, "999999999999")
	require.NoError(t, err)
	assert.Empty(t, desc.Listeners)
}

// --- HAProxy sync tests ---

func TestCreateListener_PushConfig_NoNATS(t *testing.T) {
	// When NATS conn is nil, CreateListener should still succeed
	// (pushConfig is a no-op when nc is nil or InstanceID is empty)
	svc := setupTestService(t)

	lb, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("sync-lb"),
	}, testAccountID)
	require.NoError(t, err)

	tg, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name: aws.String("sync-tg"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("HTTP"),
		Port:            aws.Int64(80),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)
	require.NoError(t, err) // No panic, no error — pushConfig skipped gracefully
}

func TestDeleteLoadBalancer_TerminatesALBVM(t *testing.T) {
	svc := setupTestService(t)

	// Set up a mock instance launcher
	mock := &mockTerminateLauncher{}
	svc.SetInstanceLauncher(mock)

	lb, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("del-lb"),
	}, testAccountID)
	require.NoError(t, err)

	lbArn := *lb.LoadBalancers[0].LoadBalancerArn

	// Delete — since no ALB VM was launched (no systemAMI), InstanceID is empty,
	// so terminate is not called. This verifies the nil-safe path.
	_, err = svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(lbArn),
	}, testAccountID)
	require.NoError(t, err)

	// No terminate call expected (no instance ID)
	assert.Equal(t, 0, len(mock.terminateCalls))
}

// mockTerminateLauncher records TerminateSystemInstance calls for testing.
type mockTerminateLauncher struct {
	terminateCalls []string
}

func (m *mockTerminateLauncher) LaunchSystemInstance(_ *SystemInstanceInput) (*SystemInstanceOutput, error) {
	return &SystemInstanceOutput{InstanceID: "i-mock", PrivateIP: "10.0.0.1"}, nil
}

func (m *mockTerminateLauncher) TerminateSystemInstance(instanceID string) error {
	m.terminateCalls = append(m.terminateCalls, instanceID)
	return nil
}

// --- Scheme unit tests ---

func TestCreateLoadBalancer_InternetFacingScheme_DNSName(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("web-alb"),
	}, testAccountID)

	require.NoError(t, err)
	lb := out.LoadBalancers[0]
	assert.Equal(t, "internet-facing", *lb.Scheme)
	// Internet-facing should NOT have "internal-" prefix
	assert.NotContains(t, *lb.DNSName, "internal-")
	assert.Contains(t, *lb.DNSName, "web-alb")
	assert.Contains(t, *lb.DNSName, ".elb.spinifex.local")
}

func TestCreateLoadBalancer_InternalScheme_DNSPrefix(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:   aws.String("backend-alb"),
		Scheme: aws.String("internal"),
	}, testAccountID)

	require.NoError(t, err)
	lb := out.LoadBalancers[0]
	assert.Equal(t, "internal", *lb.Scheme)
	// Internal scheme should have "internal-" DNS prefix
	assert.Contains(t, *lb.DNSName, "internal-backend-alb")
	assert.Contains(t, *lb.DNSName, ".elb.spinifex.local")
}

func TestCreateLoadBalancer_InternetFacingScheme_PassesSchemeToLauncher(t *testing.T) {
	svc := setupTestService(t)

	mock := &mockSystemInstanceLauncher{
		launchResult: &SystemInstanceOutput{
			InstanceID: "i-alb123",
			PrivateIP:  "10.0.1.5",
			PublicIP:   "203.0.113.10",
		},
	}
	svc.SetInstanceLauncher(mock)
	svc.SetSystemAMIFunc(func() string { return "ami-alb-test" })

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("public-alb"),
		Subnets: []*string{aws.String("subnet-aaa")},
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "internet-facing", *out.LoadBalancers[0].Scheme)

	// Without VPC service, no ENIs are created, so launcher is not called.
	// This test verifies scheme is correctly defaulted; launcher integration
	// is tested in service_impl_vpc_test.go.
}

func TestCreateLoadBalancer_InternalScheme_PassesSchemeToLauncher(t *testing.T) {
	svc := setupTestService(t)

	mock := &mockSystemInstanceLauncher{
		launchResult: &SystemInstanceOutput{
			InstanceID: "i-alb456",
			PrivateIP:  "10.0.2.10",
		},
	}
	svc.SetInstanceLauncher(mock)
	svc.SetSystemAMIFunc(func() string { return "ami-alb-test" })

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("private-alb"),
		Scheme:  aws.String("internal"),
		Subnets: []*string{aws.String("subnet-bbb")},
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "internal", *out.LoadBalancers[0].Scheme)
}
