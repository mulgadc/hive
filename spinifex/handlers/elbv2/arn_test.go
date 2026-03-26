package handlers_elbv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildLBArn(t *testing.T) {
	arn := buildLBArn("us-east-1", "123456789012", "my-alb", "50dc6c495c0c9188")
	assert.Equal(t, "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188", arn)
}

func TestBuildTGArn(t *testing.T) {
	arn := buildTGArn("us-west-2", "111222333444", "my-tg", "deadbeef")
	assert.Equal(t, "arn:aws:elasticloadbalancing:us-west-2:111222333444:targetgroup/my-tg/deadbeef", arn)
}

func TestBuildListenerArn(t *testing.T) {
	arn := buildListenerArn("eu-west-1", "999888777666", "my-alb", "lbid123", "listener456")
	assert.Equal(t, "arn:aws:elasticloadbalancing:eu-west-1:999888777666:listener/app/my-alb/lbid123/listener456", arn)
}

func TestAlbVMUserData_DefaultNatsURL(t *testing.T) {
	ud := albVMUserData("lb-abc123", "")
	assert.Contains(t, ud, "#cloud-config")
	assert.Contains(t, ud, "--lb-id=lb-abc123")
	assert.Contains(t, ud, "--nats=nats://127.0.0.1:4222")
}

func TestAlbVMUserData_CustomNatsURL(t *testing.T) {
	ud := albVMUserData("lb-xyz", "nats://10.0.0.5:4222")
	assert.Contains(t, ud, "--lb-id=lb-xyz")
	assert.Contains(t, ud, "--nats=nats://10.0.0.5:4222")
	assert.NotContains(t, ud, "127.0.0.1")
}
