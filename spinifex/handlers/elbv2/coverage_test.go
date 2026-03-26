package handlers_elbv2

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CreateTargetGroup: all health check fields + target type + tags ---

func TestCreateTargetGroup_AllHealthCheckFields(t *testing.T) {
	svc := setupTestService(t)

	out, err := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:                       aws.String("full-hc"),
		Port:                       aws.Int64(8080),
		Protocol:                   aws.String("HTTPS"),
		VpcId:                      aws.String("vpc-123"),
		TargetType:                 aws.String("ip"),
		HealthCheckProtocol:        aws.String("HTTPS"),
		HealthCheckPort:            aws.String("8443"),
		HealthCheckPath:            aws.String("/health"),
		HealthCheckIntervalSeconds: aws.Int64(15),
		HealthCheckTimeoutSeconds:  aws.Int64(5),
		HealthyThresholdCount:      aws.Int64(3),
		UnhealthyThresholdCount:    aws.Int64(2),
		Matcher:                    &elbv2.Matcher{HttpCode: aws.String("200")},
		Tags: []*elbv2.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("platform")},
		},
	}, testAccountID)

	require.NoError(t, err)
	tg := out.TargetGroups[0]
	assert.Equal(t, "ip", *tg.TargetType)
	assert.Equal(t, "HTTPS", *tg.HealthCheckProtocol)
	assert.Equal(t, "8443", *tg.HealthCheckPort)
	assert.Equal(t, "/health", *tg.HealthCheckPath)
	assert.Equal(t, int64(15), *tg.HealthCheckIntervalSeconds)
	assert.Equal(t, int64(5), *tg.HealthCheckTimeoutSeconds)
	assert.Equal(t, int64(3), *tg.HealthyThresholdCount)
	assert.Equal(t, int64(2), *tg.UnhealthyThresholdCount)
	assert.Equal(t, "200", *tg.Matcher.HttpCode)
	assert.Equal(t, int64(8080), *tg.Port)
	assert.Equal(t, "vpc-123", *tg.VpcId)
}

// --- DescribeLoadBalancers: ARN filter ---

func TestDescribeLoadBalancers_FilterByArn(t *testing.T) {
	svc := setupTestService(t)

	out1, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-arn-a")}, testAccountID)
	_, _ = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-arn-b")}, testAccountID)

	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []*string{out1.LoadBalancers[0].LoadBalancerArn},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.LoadBalancers, 1)
	assert.Equal(t, "lb-arn-a", *desc.LoadBalancers[0].LoadBalancerName)
}

// --- DescribeTargetGroups: by name ---

func TestDescribeTargetGroups_FilterByName(t *testing.T) {
	svc := setupTestService(t)

	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-alpha")}, testAccountID)
	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-beta")}, testAccountID)

	desc, err := svc.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		Names: []*string{aws.String("tg-alpha")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.TargetGroups, 1)
	assert.Equal(t, "tg-alpha", *desc.TargetGroups[0].TargetGroupName)
}

// --- DescribeTargetGroups: by ARN ---

func TestDescribeTargetGroups_FilterByArn(t *testing.T) {
	svc := setupTestService(t)

	out1, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-arn-a")}, testAccountID)
	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-arn-b")}, testAccountID)

	desc, err := svc.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		TargetGroupArns: []*string{out1.TargetGroups[0].TargetGroupArn},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.TargetGroups, 1)
	assert.Equal(t, "tg-arn-a", *desc.TargetGroups[0].TargetGroupName)
}

// --- DescribeListeners: filter by listener ARN ---

func TestDescribeListeners_FilterByListenerArn(t *testing.T) {
	svc := setupTestService(t)

	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-larn")}, testAccountID)
	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-larn")}, testAccountID)

	l1, _ := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		Port:            aws.Int64(80),
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)
	_, _ = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		Port:            aws.Int64(443),
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)

	desc, err := svc.DescribeListeners(&elbv2.DescribeListenersInput{
		ListenerArns: []*string{l1.Listeners[0].ListenerArn},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.Listeners, 1)
	assert.Equal(t, int64(80), *desc.Listeners[0].Port)
}

// --- RegisterTargets: nil target id skipped ---

func TestRegisterTargets_NilTargetSkipped(t *testing.T) {
	svc := setupTestService(t)

	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-nil"), Port: aws.Int64(80)}, testAccountID)

	_, err := svc.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: tg.TargetGroups[0].TargetGroupArn,
		Targets: []*elbv2.TargetDescription{
			{Id: nil},
			{Id: aws.String("i-valid")},
		},
	}, testAccountID)
	require.NoError(t, err)

	health, _ := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{
		TargetGroupArn: tg.TargetGroups[0].TargetGroupArn,
	}, testAccountID)
	require.Len(t, health.TargetHealthDescriptions, 1)
	assert.Equal(t, "i-valid", *health.TargetHealthDescriptions[0].Target.Id)
}

// --- RegisterTargets / DeregisterTargets: missing ARN ---

func TestRegisterTargets_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.RegisterTargets(&elbv2.RegisterTargetsInput{}, testAccountID)
	assert.Error(t, err)
}

func TestDeregisterTargets_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeregisterTargets(&elbv2.DeregisterTargetsInput{}, testAccountID)
	assert.Error(t, err)
}

func TestDeregisterTargets_TGNotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeregisterTargets(&elbv2.DeregisterTargetsInput{
		TargetGroupArn: aws.String("arn:nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
}

// --- CreateListener: custom protocol ---

func TestCreateListener_CustomProtocol(t *testing.T) {
	svc := setupTestService(t)

	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-proto")}, testAccountID)
	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-proto")}, testAccountID)

	out, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		Protocol:        aws.String("HTTPS"),
		Port:            aws.Int64(443),
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "HTTPS", *out.Listeners[0].Protocol)
	assert.Equal(t, int64(443), *out.Listeners[0].Port)
}

// --- CreateListener: missing actions ---

func TestCreateListener_MissingActions(t *testing.T) {
	svc := setupTestService(t)
	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-noact")}, testAccountID)

	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	assert.Error(t, err)
}

// --- DeleteTargetGroup: missing ARN ---

func TestDeleteTargetGroup_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{}, testAccountID)
	assert.Error(t, err)
}

// --- DeleteListener: missing ARN ---

func TestDeleteListener_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeleteListener(&elbv2.DeleteListenerInput{}, testAccountID)
	assert.Error(t, err)
}

// --- DescribeTargetHealth: missing ARN ---

func TestDescribeTargetHealth_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{}, testAccountID)
	assert.Error(t, err)
}

// --- DescribeLoadBalancers: empty filters returns all ---

func TestDescribeLoadBalancers_All(t *testing.T) {
	svc := setupTestService(t)

	_, _ = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-all-a")}, testAccountID)
	_, _ = svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-all-b")}, testAccountID)

	desc, err := svc.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{}, testAccountID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.LoadBalancers), 2)
}

// --- DescribeTargetGroups: all ---

func TestDescribeTargetGroups_All(t *testing.T) {
	svc := setupTestService(t)

	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-all-a")}, testAccountID)
	_, _ = svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-all-b")}, testAccountID)

	desc, err := svc.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{}, testAccountID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.TargetGroups), 2)
}

// --- DescribeListeners: all (no LB ARN filter) ---

func TestDescribeListeners_All(t *testing.T) {
	svc := setupTestService(t)

	lb, _ := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{Name: aws.String("lb-lall")}, testAccountID)
	tg, _ := svc.CreateTargetGroup(&elbv2.CreateTargetGroupInput{Name: aws.String("tg-lall")}, testAccountID)
	_, _ = svc.CreateListener(&elbv2.CreateListenerInput{
		LoadBalancerArn: lb.LoadBalancers[0].LoadBalancerArn,
		DefaultActions:  []*elbv2.Action{{Type: aws.String("forward"), TargetGroupArn: tg.TargetGroups[0].TargetGroupArn}},
	}, testAccountID)

	desc, err := svc.DescribeListeners(&elbv2.DescribeListenersInput{}, testAccountID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.Listeners), 1)
}

// --- DeleteLoadBalancer: missing ARN ---

func TestDeleteLoadBalancer_MissingArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{}, testAccountID)
	assert.Error(t, err)
}

// --- RegisterTargets: TG not found ---

func TestRegisterTargets_TGNotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: aws.String("arn:nonexistent"),
		Targets:        []*elbv2.TargetDescription{{Id: aws.String("i-abc")}},
	}, testAccountID)
	assert.Error(t, err)
}

// --- CreateListener: missing LB ARN ---

func TestCreateListener_MissingLBArn(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreateListener(&elbv2.CreateListenerInput{
		DefaultActions: []*elbv2.Action{{Type: aws.String("forward")}},
	}, testAccountID)
	assert.Error(t, err)
}
