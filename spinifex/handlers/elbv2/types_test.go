package handlers_elbv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultHealthCheck(t *testing.T) {
	hc := DefaultHealthCheck()

	assert.Equal(t, ProtocolHTTP, hc.Protocol)
	assert.Equal(t, DefaultHealthCheckPort, hc.Port)
	assert.Equal(t, DefaultHealthCheckPath, hc.Path)
	assert.Equal(t, int64(DefaultHealthCheckInterval), hc.IntervalSeconds)
	assert.Equal(t, int64(DefaultHealthCheckTimeout), hc.TimeoutSeconds)
	assert.Equal(t, int64(DefaultHealthyThreshold), hc.HealthyThreshold)
	assert.Equal(t, int64(DefaultUnhealthyThreshold), hc.UnhealthyThreshold)
	assert.Equal(t, DefaultHealthCheckMatcher, hc.Matcher)
}

func TestHealthCheckConfig_CustomValues(t *testing.T) {
	hc := HealthCheckConfig{
		Protocol:           ProtocolHTTPS,
		Port:               "8443",
		Path:               "/healthz",
		IntervalSeconds:    10,
		TimeoutSeconds:     3,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
		Matcher:            "200-299",
	}

	assert.Equal(t, ProtocolHTTPS, hc.Protocol)
	assert.Equal(t, "8443", hc.Port)
	assert.Equal(t, "/healthz", hc.Path)
	assert.Equal(t, int64(10), hc.IntervalSeconds)
}

func TestLoadBalancerRecord_Fields(t *testing.T) {
	lb := LoadBalancerRecord{
		Name:           "my-alb",
		Scheme:         SchemeInternetFacing,
		Type:           LoadBalancerTypeApplication,
		State:          StateProvisioning,
		SecurityGroups: []string{"sg-111", "sg-222"},
		Subnets:        []string{"subnet-aaa", "subnet-bbb"},
		IPAddressType:  IPAddressTypeIPv4,
	}

	assert.Equal(t, "internet-facing", lb.Scheme)
	assert.Equal(t, "application", lb.Type)
	assert.Equal(t, "provisioning", lb.State)
	assert.Len(t, lb.SecurityGroups, 2)
	assert.Len(t, lb.Subnets, 2)
}

func TestTarget_Fields(t *testing.T) {
	target := Target{
		Id:          "i-abc123",
		Port:        8080,
		HealthState: TargetHealthHealthy,
		HealthDesc:  "Target is healthy",
		PrivateIP:   "10.0.1.50",
	}

	assert.Equal(t, "i-abc123", target.Id)
	assert.Equal(t, int64(8080), target.Port)
	assert.Equal(t, "healthy", target.HealthState)
}

func TestListenerAction_Fields(t *testing.T) {
	action := ListenerAction{
		Type:           ActionTypeForward,
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/my-tg/abc123",
	}

	assert.Equal(t, "forward", action.Type)
	assert.Contains(t, action.TargetGroupArn, "targetgroup")
}
