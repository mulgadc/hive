package handlers_elbv2

import (
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestService(t *testing.T) *ELBv2ServiceImpl {
	t.Helper()
	_, nc, _ := testutil.StartTestJetStream(t)

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

func TestCreateLoadBalancer_NetworkType(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("my-nlb"),
		Type: aws.String("network"),
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.LoadBalancers, 1)
	lb := out.LoadBalancers[0]
	assert.Equal(t, "network", *lb.Type)
	assert.Contains(t, *lb.LoadBalancerArn, "loadbalancer/net/my-nlb")
	assert.Equal(t, "active", *lb.State.Code)
}

func TestCreateLoadBalancer_NetworkType_RejectsSecurityGroups(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:           aws.String("nlb-with-sg"),
		Type:           aws.String("network"),
		SecurityGroups: []*string{aws.String("sg-111")},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidConfigurationRequest")
}

func TestCreateLoadBalancer_CrossZoneAttributes(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-cz"),
		Type: aws.String("network"),
	}, testAccountID)

	require.NoError(t, err)
	// NLB should have no seeded attributes (falls through to default false)
	lb, err := svc.store.GetLoadBalancerByName("nlb-cz", testAccountID)
	require.NoError(t, err)
	assert.Nil(t, lb.Attributes)

	// ALB should seed cross-zone enabled=true in attributes
	_, err = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("alb-cz"),
	}, testAccountID)
	require.NoError(t, err)
	albRec, err := svc.store.GetLoadBalancerByName("alb-cz", testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "true", albRec.Attributes["load_balancing.cross_zone.enabled"])

	_ = out // suppress unused warning
}

func TestCreateLoadBalancer_InvalidType(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("bad-type"),
		Type: aws.String("gateway"),
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateLoadBalancer_ALB_AllowsSecurityGroups(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:           aws.String("alb-with-sg"),
		SecurityGroups: []*string{aws.String("sg-111")},
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "application", *out.LoadBalancers[0].Type)
	assert.Contains(t, *out.LoadBalancers[0].LoadBalancerArn, "loadbalancer/app/alb-with-sg")
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

func TestCreateTargetGroup_TCPProtocol(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tcp-tg"),
		Protocol: aws.String("TCP"),
		Port:     aws.Int64(5432),
		VpcId:    aws.String("vpc-test"),
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.TargetGroups, 1)
	tg := out.TargetGroups[0]
	assert.Equal(t, "tcp-tg", *tg.TargetGroupName)
	assert.Equal(t, "TCP", *tg.Protocol)
	assert.Equal(t, int64(5432), *tg.Port)
	// NLB health check defaults: TCP protocol, no path, no matcher.
	assert.Equal(t, "TCP", *tg.HealthCheckProtocol)
	assert.Equal(t, "", *tg.HealthCheckPath)
	assert.Equal(t, "", *tg.Matcher.HttpCode)
	assert.Equal(t, int64(10), *tg.HealthCheckTimeoutSeconds)
	assert.Equal(t, int64(3), *tg.HealthyThresholdCount)
	assert.Equal(t, int64(3), *tg.UnhealthyThresholdCount)
}

func TestCreateTargetGroup_NLBProtocols(t *testing.T) {
	for _, proto := range []string{"TCP", "UDP", "TLS", "TCP_UDP"} {
		t.Run(proto, func(t *testing.T) {
			svc := setupTestService(t)

			out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
				Name:     aws.String("tg-" + proto),
				Protocol: aws.String(proto),
				Port:     aws.Int64(8080),
			}, testAccountID)

			require.NoError(t, err)
			require.Len(t, out.TargetGroups, 1)
			assert.Equal(t, proto, *out.TargetGroups[0].Protocol)
			// All NLB protocols get NLB health check defaults.
			assert.Equal(t, "TCP", *out.TargetGroups[0].HealthCheckProtocol)
		})
	}
}

func TestCreateTargetGroup_InvalidProtocol(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("bad-proto-tg"),
		Protocol: aws.String("SCTP"),
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateTargetGroup_TCPWithCustomHealthCheck(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:                       aws.String("tcp-custom-hc"),
		Protocol:                   aws.String("TCP"),
		Port:                       aws.Int64(3306),
		HealthCheckProtocol:        aws.String("HTTP"),
		HealthCheckPath:            aws.String("/health"),
		HealthCheckIntervalSeconds: aws.Int64(15),
		Matcher:                    &elbv2.Matcher{HttpCode: aws.String("200")},
	}, testAccountID)

	require.NoError(t, err)
	tg := out.TargetGroups[0]
	assert.Equal(t, "TCP", *tg.Protocol)
	// User overrides should take effect even on NLB target groups.
	assert.Equal(t, "HTTP", *tg.HealthCheckProtocol)
	assert.Equal(t, "/health", *tg.HealthCheckPath)
	assert.Equal(t, "200", *tg.Matcher.HttpCode)
	assert.Equal(t, int64(15), *tg.HealthCheckIntervalSeconds)
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

// --- NLB Listener tests ---

func TestCreateListener_NLB_TCPProtocol(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-tcp-lst"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tcp-tg-lst"),
		Protocol: aws.String("TCP"),
		Port:     aws.Int64(5432),
	}, testAccountID)

	out, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("TCP"),
		Port:            aws.Int64(5432),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	require.NoError(t, err)
	require.Len(t, out.Listeners, 1)
	l := out.Listeners[0]
	assert.Equal(t, "TCP", *l.Protocol)
	assert.Equal(t, int64(5432), *l.Port)
	assert.Equal(t, *lbOut.LoadBalancers[0].LoadBalancerArn, *l.LoadBalancerArn)
}

func TestCreateListener_NLB_AllProtocols(t *testing.T) {
	for _, proto := range []string{"TCP", "UDP", "TLS", "TCP_UDP"} {
		t.Run(proto, func(t *testing.T) {
			svc := setupTestService(t)

			lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
				Name: aws.String("nlb-" + proto),
				Type: aws.String("network"),
			}, testAccountID)
			tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
				Name:     aws.String("tg-" + proto),
				Protocol: aws.String(proto),
				Port:     aws.Int64(8080),
			}, testAccountID)

			out, err := svc.CreateListener(&elbv2.CreateListenerInput{
				LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
				Protocol:        aws.String(proto),
				Port:            aws.Int64(8080),
				DefaultActions: []*elbv2.Action{
					{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
				},
			}, testAccountID)

			require.NoError(t, err)
			assert.Equal(t, proto, *out.Listeners[0].Protocol)
		})
	}
}

func TestCreateListener_NLB_RejectsHTTPProtocol(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-no-http"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name: aws.String("tg-http-nlb"),
	}, testAccountID)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("HTTP"),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateListener_NLB_RejectsHTTPSProtocol(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-no-https"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name: aws.String("tg-https-nlb"),
	}, testAccountID)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("HTTPS"),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateListener_ALB_RejectsTCPProtocol(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("alb-no-tcp"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tg-tcp-alb"),
		Protocol: aws.String("TCP"),
		Port:     aws.Int64(8080),
	}, testAccountID)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("TCP"),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateListener_NLB_ProtocolCompatibility_TLSToTCP(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-tls-tcp"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tg-tcp-compat"),
		Protocol: aws.String("TCP"),
		Port:     aws.Int64(443),
	}, testAccountID)

	// TLS listener -> TCP target group is valid
	out, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("TLS"),
		Port:            aws.Int64(443),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "TLS", *out.Listeners[0].Protocol)
}

func TestCreateListener_NLB_ProtocolIncompatible_TCPToUDP(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-tcp-udp"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tg-udp-incompat"),
		Protocol: aws.String("UDP"),
		Port:     aws.Int64(53),
	}, testAccountID)

	// TCP listener -> UDP target group is invalid
	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("TCP"),
		Port:            aws.Int64(53),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateListener_NLB_ProtocolIncompatible_UDPToTCP(t *testing.T) {
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-udp-tcp"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:     aws.String("tg-tcp-incompat"),
		Protocol: aws.String("TCP"),
		Port:     aws.Int64(8080),
	}, testAccountID)

	// UDP listener -> TCP target group is invalid
	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("UDP"),
		Port:            aws.Int64(8080),
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestCreateListener_NLB_DefaultProtocol_Rejected(t *testing.T) {
	// When no protocol is specified, it defaults to HTTP which is invalid for NLB
	svc := setupTestService(t)

	lbOut, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-default-proto"),
		Type: aws.String("network"),
	}, testAccountID)
	tgOut, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name: aws.String("tg-default-proto"),
	}, testAccountID)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		DefaultActions: []*elbv2.Action{
			{Type: aws.String("forward"), TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn},
		},
	}, testAccountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

// --- HAProxy sync tests ---

func TestCreateListener_PushConfig_NoNATS(t *testing.T) {
	// When NATS conn is nil, CreateListener should still succeed
	// (updateStoredConfig is a no-op when InstanceID is empty)
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
	require.NoError(t, err) // No panic, no error — updateStoredConfig skipped gracefully
}

func TestDeleteLoadBalancer_TerminatesALBVM(t *testing.T) {
	svc := setupTestService(t)

	// Set up a mock instance launcher
	mock := &mockTerminateLauncher{}
	svc.InstanceLauncher = mock

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
	svc.InstanceLauncher = mock
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
	svc.InstanceLauncher = mock
	svc.SetSystemAMIFunc(func() string { return "ami-alb-test" })

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("private-alb"),
		Scheme:  aws.String("internal"),
		Subnets: []*string{aws.String("subnet-bbb")},
	}, testAccountID)

	require.NoError(t, err)
	assert.Equal(t, "internal", *out.LoadBalancers[0].Scheme)
}

// --- LBAgentHeartbeat tests ---

func TestLBAgentHeartbeat_TransitionsProvisioningToActive(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/hb-lb/lb-hb1",
		LoadBalancerID:  "lb-hb1",
		Name:            "hb-lb",
		State:           StateProvisioning,
		InstanceID:      "i-sys-hb1",
		VPCIP:           "10.0.1.100",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	out, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-hb1"),
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, *out.Status)

	stored, err := svc.store.GetLoadBalancer("lb-hb1")
	require.NoError(t, err)
	assert.Equal(t, StateActive, stored.State)
	assert.False(t, stored.LastHeartbeat.IsZero())
}

func TestLBAgentHeartbeat_ReturnsConfigHash(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/hash-lb/lb-hash1",
		LoadBalancerID:  "lb-hash1",
		Name:            "hash-lb",
		State:           StateActive,
		InstanceID:      "i-sys-hash1",
		ConfigHash:      "abc123def456",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	out, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-hash1"),
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "abc123def456", *out.ConfigHash)
}

func TestLBAgentHeartbeat_ProcessesHealthReport(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/health-lb/lb-hr1",
		LoadBalancerID:  "lb-hr1",
		Name:            "health-lb",
		State:           StateActive,
		InstanceID:      "i-sys-hr1",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	tg := &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/health-tg/tg-hr1",
		TargetGroupID:  "tg-hr1",
		Port:           80,
		HealthCheck:    DefaultHealthCheck(),
		Targets: []Target{
			{Id: "i-target1", Port: 80, HealthState: TargetHealthInitial, PrivateIP: "10.0.1.20"},
		},
		AccountID: testAccountID,
	}
	require.NoError(t, svc.store.PutTargetGroup(tg))

	// Wire LB → listener → TG so the health checker can resolve the TG from the LBID.
	require.NoError(t, svc.store.PutListener(&ListenerRecord{
		ListenerArn:     lb.LoadBalancerArn + "/listener-1",
		ListenerID:      "lst-hr1",
		LoadBalancerArn: lb.LoadBalancerArn,
		Protocol:        "HTTP",
		Port:            80,
		DefaultActions:  []ListenerAction{{Type: ActionTypeForward, TargetGroupArn: tg.TargetGroupArn}},
		AccountID:       testAccountID,
	}))

	srvName := sanitizeName("srv", "i-target1")
	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-hr1"),
		Servers: []*LBAgentServerStatus{
			{Backend: aws.String("bk_tg-hr1"), Server: aws.String(srvName), Status: aws.String("UP")},
		},
	}, testAccountID)
	require.NoError(t, err)

	stored, err := svc.store.GetTargetGroup("tg-hr1")
	require.NoError(t, err)
	assert.Equal(t, TargetHealthHealthy, stored.Targets[0].HealthState)
}

func TestLBAgentHeartbeat_MissingLBID(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{}, testAccountID)
	assert.Error(t, err)
}

func TestLBAgentHeartbeat_LBNotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
}

// --- GetLBConfig tests ---

func TestGetLBConfig_ReturnsStoredConfig(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/cfg-lb/lb-cfg1",
		LoadBalancerID:  "lb-cfg1",
		Name:            "cfg-lb",
		State:           StateActive,
		InstanceID:      "i-sys-cfg1",
		ConfigText:      "global\n    log stdout\n",
		ConfigHash:      "deadbeef",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	out, err := svc.GetLBConfig(&GetLBConfigInput{
		LBID: aws.String("lb-cfg1"),
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "global\n    log stdout\n", *out.ConfigText)
	assert.Equal(t, "deadbeef", *out.ConfigHash)
}

func TestGetLBConfig_MissingLBID(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.GetLBConfig(&GetLBConfigInput{}, testAccountID)
	assert.Error(t, err)
}

func TestGetLBConfig_LBNotFound(t *testing.T) {
	svc := setupTestService(t)

	_, err := svc.GetLBConfig(&GetLBConfigInput{
		LBID: aws.String("lb-missing"),
	}, testAccountID)
	assert.Error(t, err)
}

func TestLBAgentHeartbeat_WrongAccount(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/auth-lb/lb-auth1",
		LoadBalancerID:  "lb-auth1",
		Name:            "auth-lb",
		State:           StateActive,
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-auth1"),
	}, "999999999999")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LoadBalancerNotFound")
}

func TestLBAgentHeartbeat_SystemAccountAllowed(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/sys-lb/lb-sys1",
		LoadBalancerID:  "lb-sys1",
		Name:            "sys-lb",
		State:           StateActive,
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	out, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-sys1"),
	}, utils.GlobalAccountID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, *out.Status)
}

func TestLBAgentHeartbeat_SkipsWriteWhenHeartbeatFresh(t *testing.T) {
	svc := setupTestService(t)

	recentHeartbeat := time.Now().UTC().Add(-10 * time.Second) // well within 60s threshold
	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/fresh-lb/lb-fresh1",
		LoadBalancerID:  "lb-fresh1",
		Name:            "fresh-lb",
		State:           StateActive,
		LastHeartbeat:   recentHeartbeat,
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-fresh1"),
	}, testAccountID)
	require.NoError(t, err)

	// LastHeartbeat should NOT have been updated because the stored value is fresh.
	stored, err := svc.store.GetLoadBalancer("lb-fresh1")
	require.NoError(t, err)
	assert.Equal(t, recentHeartbeat, stored.LastHeartbeat)
}

func TestLBAgentHeartbeat_PersistsWhenHeartbeatStale(t *testing.T) {
	svc := setupTestService(t)

	staleHeartbeat := time.Now().UTC().Add(-2 * time.Minute) // beyond 60s threshold
	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/stale-lb/lb-stale1",
		LoadBalancerID:  "lb-stale1",
		Name:            "stale-lb",
		State:           StateActive,
		LastHeartbeat:   staleHeartbeat,
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	_, err := svc.LBAgentHeartbeat(&LBAgentHeartbeatInput{
		LBID: aws.String("lb-stale1"),
	}, testAccountID)
	require.NoError(t, err)

	// LastHeartbeat should have been refreshed.
	stored, err := svc.store.GetLoadBalancer("lb-stale1")
	require.NoError(t, err)
	assert.True(t, stored.LastHeartbeat.After(staleHeartbeat))
}

func TestGetLBConfig_WrongAccount(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/auth-cfg/lb-authcfg1",
		LoadBalancerID:  "lb-authcfg1",
		Name:            "auth-cfg",
		State:           StateActive,
		ConfigText:      "global\n",
		ConfigHash:      "aaa",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	_, err := svc.GetLBConfig(&GetLBConfigInput{
		LBID: aws.String("lb-authcfg1"),
	}, "999999999999")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LoadBalancerNotFound")
}

func TestGetLBConfig_SystemAccountAllowed(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/sys-cfg/lb-syscfg1",
		LoadBalancerID:  "lb-syscfg1",
		Name:            "sys-cfg",
		State:           StateActive,
		ConfigText:      "global\n    log stdout\n",
		ConfigHash:      "bbb",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	out, err := svc.GetLBConfig(&GetLBConfigInput{
		LBID: aws.String("lb-syscfg1"),
	}, utils.GlobalAccountID)
	require.NoError(t, err)
	assert.Equal(t, "global\n    log stdout\n", *out.ConfigText)
}

// --- updateStoredConfig tests ---

func TestUpdateStoredConfig_StoresConfigAndHash(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/upd-lb/lb-upd1",
		LoadBalancerID:  "lb-upd1",
		Name:            "upd-lb",
		State:           StateActive,
		InstanceID:      "i-sys-upd1",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	tg := &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/upd-tg/tg-upd1",
		TargetGroupID:  "tg-upd1",
		Port:           80,
		HealthCheck:    DefaultHealthCheck(),
		Targets: []Target{
			{Id: "i-srv1", Port: 80, HealthState: TargetHealthHealthy, PrivateIP: "10.0.1.30"},
		},
		AccountID: testAccountID,
	}
	require.NoError(t, svc.store.PutTargetGroup(tg))

	listener := &ListenerRecord{
		ListenerArn:     "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/upd-lb/lb-upd1/lst-upd1",
		ListenerID:      "lst-upd1",
		LoadBalancerArn: lb.LoadBalancerArn,
		Protocol:        ProtocolHTTP,
		Port:            80,
		DefaultActions:  []ListenerAction{{Type: ActionTypeForward, TargetGroupArn: tg.TargetGroupArn}},
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutListener(listener))

	require.NoError(t, svc.updateStoredConfig(lb))

	stored, err := svc.store.GetLoadBalancer("lb-upd1")
	require.NoError(t, err)
	assert.NotEmpty(t, stored.ConfigText)
	assert.NotEmpty(t, stored.ConfigHash)
	assert.Len(t, stored.ConfigHash, 64) // SHA256 hex
}

func TestUpdateStoredConfig_SkipsWhenNoInstance(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/noinst/lb-noinst",
		LoadBalancerID:  "lb-noinst",
		Name:            "noinst-lb",
		State:           StateActive,
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	require.NoError(t, svc.updateStoredConfig(lb))

	stored, err := svc.store.GetLoadBalancer("lb-noinst")
	require.NoError(t, err)
	assert.Empty(t, stored.ConfigText)
	assert.Empty(t, stored.ConfigHash)
}

// --- Service lifecycle and setter tests ---

func TestClose(t *testing.T) {
	svc := setupTestService(t)
	// Close should not panic; stops health checker and cancels context.
	svc.Close()
}

func TestSystemCredentialsFields(t *testing.T) {
	svc := setupTestService(t)
	svc.SystemAccessKey = "AKID123"
	svc.SystemSecretKey = "secret456"
	assert.Equal(t, "AKID123", svc.SystemAccessKey)
	assert.Equal(t, "secret456", svc.SystemSecretKey)
}

func TestGatewayURLField(t *testing.T) {
	svc := setupTestService(t)
	svc.GatewayURL = "https://10.15.8.1:9999"
	assert.Equal(t, "https://10.15.8.1:9999", svc.GatewayURL)
}

func TestSetSystemInstanceTypeFunc(t *testing.T) {
	svc := setupTestService(t)

	// Before setting, should return empty
	assert.Empty(t, svc.getSystemInstanceType())

	// Set the resolver
	svc.SetSystemInstanceTypeFunc(func() string { return "t3.micro" })
	assert.Equal(t, "t3.micro", svc.getSystemInstanceType())

	// Once resolved, caches the value
	svc.systemInstanceTypeFunc = func() string { return "t3.large" }
	assert.Equal(t, "t3.micro", svc.getSystemInstanceType())
}

func TestGetSystemAMI(t *testing.T) {
	svc := setupTestService(t)

	// Before setting, should return empty
	assert.Empty(t, svc.getSystemAMI())

	// Set the resolver
	svc.SetSystemAMIFunc(func() string { return "ami-system-001" })
	assert.Equal(t, "ami-system-001", svc.getSystemAMI())

	// Once resolved, caches the value
	svc.systemAMIFunc = func() string { return "ami-system-002" }
	assert.Equal(t, "ami-system-001", svc.getSystemAMI())
}

func TestGetSystemAMI_RetryOnEmpty(t *testing.T) {
	svc := setupTestService(t)

	calls := 0
	svc.SetSystemAMIFunc(func() string {
		calls++
		if calls < 3 {
			return "" // simulate image not imported yet
		}
		return "ami-system-001"
	})

	// First two calls return empty — should NOT be cached
	assert.Empty(t, svc.getSystemAMI())
	assert.Empty(t, svc.getSystemAMI())
	assert.Equal(t, 2, calls)

	// Third call finds the image — should be cached from now on
	assert.Equal(t, "ami-system-001", svc.getSystemAMI())
	assert.Equal(t, 3, calls)

	// Subsequent calls return cached value without calling the func again
	assert.Equal(t, "ami-system-001", svc.getSystemAMI())
	assert.Equal(t, 3, calls)
}

func TestGetSystemInstanceType_RetryOnEmpty(t *testing.T) {
	svc := setupTestService(t)

	calls := 0
	svc.SetSystemInstanceTypeFunc(func() string {
		calls++
		if calls < 2 {
			return ""
		}
		return "t3.micro"
	})

	assert.Empty(t, svc.getSystemInstanceType())
	assert.Equal(t, 1, calls)

	assert.Equal(t, "t3.micro", svc.getSystemInstanceType())
	assert.Equal(t, 2, calls)

	// Cached
	assert.Equal(t, "t3.micro", svc.getSystemInstanceType())
	assert.Equal(t, 2, calls)
}

func TestGetSystemAMI_Concurrent(t *testing.T) {
	svc := setupTestService(t)
	svc.SetSystemAMIFunc(func() string { return "ami-system-001" })

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			got := svc.getSystemAMI()
			assert.Equal(t, "ami-system-001", got)
		})
	}
	wg.Wait()
}

func TestGetSystemInstanceType_Concurrent(t *testing.T) {
	svc := setupTestService(t)
	svc.SetSystemInstanceTypeFunc(func() string { return "t3.micro" })

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			got := svc.getSystemInstanceType()
			assert.Equal(t, "t3.micro", got)
		})
	}
	wg.Wait()
}

func TestMgmtRouteFields(t *testing.T) {
	svc := setupTestService(t)
	svc.MgmtRouteGateway = "10.15.8.1"
	svc.MgmtRouteTarget = "10.15.8.100"
	assert.Equal(t, "10.15.8.1", svc.MgmtRouteGateway)
	assert.Equal(t, "10.15.8.100", svc.MgmtRouteTarget)
}

func TestLBVMUserData_MgmtRoute(t *testing.T) {
	svc := setupTestService(t)
	svc.MgmtRouteGateway = "10.15.8.1"
	svc.MgmtRouteTarget = "10.15.8.100"
	svc.GatewayURL = "https://10.15.8.100:9999"
	svc.SystemAccessKey = "AK"
	svc.SystemSecretKey = "SK"

	data, err := svc.lbVMUserData("lb-test1")
	require.NoError(t, err)
	assert.Contains(t, data, "bootcmd:")
	assert.Contains(t, data, `"10.15.8.100/32"`)
	assert.Contains(t, data, `"10.15.8.1"`)
}

func TestIsCompatibleProtocol_UnknownListenerProtocol(t *testing.T) {
	assert.False(t, isCompatibleProtocol("SCTP", ProtocolTCP))
	assert.False(t, isCompatibleProtocol("", ProtocolHTTP))
}

func TestCreateListener_TargetGroupNotFound(t *testing.T) {
	svc := setupTestService(t)

	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("tg-missing-lb"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String(ProtocolHTTP),
		Port:            aws.Int64(80),
		DefaultActions: []*elbv2.Action{
			{
				Type:           aws.String(ActionTypeForward),
				TargetGroupArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/no-exist/tg-nope"),
			},
		},
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "TargetGroupNotFound")
}

func TestUpdateStoredConfig_MissingTargetGroup(t *testing.T) {
	svc := setupTestService(t)

	lb := &LoadBalancerRecord{
		LoadBalancerArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/miss-tg/lb-misstg",
		LoadBalancerID:  "lb-misstg",
		Name:            "miss-tg",
		State:           StateActive,
		InstanceID:      "i-sys-misstg",
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutLoadBalancer(lb))

	listener := &ListenerRecord{
		ListenerArn:     "arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/miss-tg/lb-misstg/lst-misstg",
		ListenerID:      "lst-misstg",
		LoadBalancerArn: lb.LoadBalancerArn,
		Protocol:        ProtocolHTTP,
		Port:            80,
		DefaultActions:  []ListenerAction{{Type: ActionTypeForward, TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/gone/tg-gone"}},
		AccountID:       testAccountID,
	}
	require.NoError(t, svc.store.PutListener(listener))

	// Should not error — skips missing TG and generates config with no backends
	require.NoError(t, svc.updateStoredConfig(lb))

	stored, err := svc.store.GetLoadBalancer("lb-misstg")
	require.NoError(t, err)
	assert.NotEmpty(t, stored.ConfigHash)
}

// --- Attribute tests ---

func TestDescribeTargetGroupAttributes_Defaults(t *testing.T) {
	svc := setupTestService(t)
	tgOut, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-attr-defaults")}, testAccountID)
	require.NoError(t, err)

	out, err := svc.DescribeTargetGroupAttributes(&elbv2.DescribeTargetGroupAttributesInput{
		TargetGroupArn: tgOut.TargetGroups[0].TargetGroupArn,
	}, testAccountID)
	require.NoError(t, err)

	defaults := DefaultTargetGroupAttributes()
	assert.Len(t, out.Attributes, len(defaults))
	attrMap := make(map[string]string)
	for _, a := range out.Attributes {
		attrMap[*a.Key] = *a.Value
	}
	for k, v := range defaults {
		assert.Equal(t, v, attrMap[k], "default mismatch for key %s", k)
	}
}

func TestDescribeLoadBalancerAttributes_ALBDefaults(t *testing.T) {
	svc := setupTestService(t)
	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("alb-attr-defaults")}, testAccountID)
	require.NoError(t, err)

	out, err := svc.DescribeLoadBalancerAttributes(&elbv2.DescribeLoadBalancerAttributesInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)

	defaults := DefaultLoadBalancerAttributes()
	assert.Len(t, out.Attributes, len(defaults))
	attrMap := make(map[string]string)
	for _, a := range out.Attributes {
		attrMap[*a.Key] = *a.Value
	}
	// ALB seeds cross-zone=true, overriding the default false
	assert.Equal(t, "true", attrMap["load_balancing.cross_zone.enabled"])
}

func TestDescribeLoadBalancerAttributes_NLBDefaults(t *testing.T) {
	svc := setupTestService(t)
	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name: aws.String("nlb-attr-defaults"),
		Type: aws.String("network"),
	}, testAccountID)
	require.NoError(t, err)

	out, err := svc.DescribeLoadBalancerAttributes(&elbv2.DescribeLoadBalancerAttributesInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)

	attrMap := make(map[string]string)
	for _, a := range out.Attributes {
		attrMap[*a.Key] = *a.Value
	}
	// NLB should fall through to default false
	assert.Equal(t, "false", attrMap["load_balancing.cross_zone.enabled"])
}

func TestModifyDescribeTargetGroupAttributes_RoundTrip(t *testing.T) {
	svc := setupTestService(t)
	tgOut, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-attr-rt")}, testAccountID)
	require.NoError(t, err)
	arn := tgOut.TargetGroups[0].TargetGroupArn

	modOut, err := svc.ModifyTargetGroupAttributes(&elbv2.ModifyTargetGroupAttributesInput{
		TargetGroupArn: arn,
		Attributes: []*elbv2.TargetGroupAttribute{
			{Key: aws.String("deregistration_delay.timeout_seconds"), Value: aws.String("60")},
			{Key: aws.String("stickiness.enabled"), Value: aws.String("true")},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, modOut.Attributes, 2)

	descOut, err := svc.DescribeTargetGroupAttributes(&elbv2.DescribeTargetGroupAttributesInput{
		TargetGroupArn: arn,
	}, testAccountID)
	require.NoError(t, err)
	attrMap := make(map[string]string)
	for _, a := range descOut.Attributes {
		attrMap[*a.Key] = *a.Value
	}
	assert.Equal(t, "60", attrMap["deregistration_delay.timeout_seconds"])
	assert.Equal(t, "true", attrMap["stickiness.enabled"])
	// Unmodified defaults should still be present
	assert.Equal(t, "lb_cookie", attrMap["stickiness.type"])
}

func TestModifyDescribeLoadBalancerAttributes_RoundTrip(t *testing.T) {
	svc := setupTestService(t)
	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-attr-rt")}, testAccountID)
	require.NoError(t, err)
	arn := lbOut.LoadBalancers[0].LoadBalancerArn

	modOut, err := svc.ModifyLoadBalancerAttributes(&elbv2.ModifyLoadBalancerAttributesInput{
		LoadBalancerArn: arn,
		Attributes: []*elbv2.LoadBalancerAttribute{
			{Key: aws.String("idle_timeout.timeout_seconds"), Value: aws.String("120")},
			{Key: aws.String("deletion_protection.enabled"), Value: aws.String("true")},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, modOut.Attributes, 2)

	descOut, err := svc.DescribeLoadBalancerAttributes(&elbv2.DescribeLoadBalancerAttributesInput{
		LoadBalancerArn: arn,
	}, testAccountID)
	require.NoError(t, err)
	attrMap := make(map[string]string)
	for _, a := range descOut.Attributes {
		attrMap[*a.Key] = *a.Value
	}
	assert.Equal(t, "120", attrMap["idle_timeout.timeout_seconds"])
	assert.Equal(t, "true", attrMap["deletion_protection.enabled"])
	// Unmodified defaults should still be present
	assert.Equal(t, "true", attrMap["routing.http2.enabled"])
}

func TestModifyTargetGroupAttributes_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.ModifyTargetGroupAttributes(&elbv2.ModifyTargetGroupAttributesInput{
		TargetGroupArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/missing/tg-missing"),
		Attributes: []*elbv2.TargetGroupAttribute{
			{Key: aws.String("stickiness.enabled"), Value: aws.String("true")},
		},
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorELBv2TargetGroupNotFound)
}

func TestDescribeTargetGroupAttributes_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribeTargetGroupAttributes(&elbv2.DescribeTargetGroupAttributesInput{
		TargetGroupArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/missing/tg-missing"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorELBv2TargetGroupNotFound)
}

func TestModifyLoadBalancerAttributes_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.ModifyLoadBalancerAttributes(&elbv2.ModifyLoadBalancerAttributesInput{
		LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/missing/lb-missing"),
		Attributes: []*elbv2.LoadBalancerAttribute{
			{Key: aws.String("idle_timeout.timeout_seconds"), Value: aws.String("30")},
		},
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorELBv2LoadBalancerNotFound)
}

func TestDescribeLoadBalancerAttributes_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribeLoadBalancerAttributes(&elbv2.DescribeLoadBalancerAttributesInput{
		LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/missing/lb-missing"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorELBv2LoadBalancerNotFound)
}

func TestModifyTargetGroupAttributes_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.ModifyTargetGroupAttributes(&elbv2.ModifyTargetGroupAttributesInput{
		Attributes: []*elbv2.TargetGroupAttribute{
			{Key: aws.String("stickiness.enabled"), Value: aws.String("true")},
		},
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeTargetGroupAttributes_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribeTargetGroupAttributes(&elbv2.DescribeTargetGroupAttributesInput{}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestModifyLoadBalancerAttributes_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.ModifyLoadBalancerAttributes(&elbv2.ModifyLoadBalancerAttributesInput{
		Attributes: []*elbv2.LoadBalancerAttribute{
			{Key: aws.String("idle_timeout.timeout_seconds"), Value: aws.String("30")},
		},
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}

func TestDescribeLoadBalancerAttributes_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribeLoadBalancerAttributes(&elbv2.DescribeLoadBalancerAttributesInput{}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorMissingParameter)
}
