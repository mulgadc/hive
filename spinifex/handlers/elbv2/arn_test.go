package handlers_elbv2

import (
	"strings"
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

func TestAlbVMUserData_ContainsLBID(t *testing.T) {
	ud := albVMUserData("lb-abc123")
	assert.Contains(t, ud, "#cloud-config")
	assert.Contains(t, ud, "write_files:")
	assert.Contains(t, ud, "/etc/conf.d/alb-agent")
	assert.Contains(t, ud, "ALB_LB_ID=lb-abc123")
	// NATS URL should no longer be present
	assert.NotContains(t, ud, "NATS")
}

func TestAlbVMUserData_WriteFilesThenRuncmd(t *testing.T) {
	// Cloud-init guarantees write_files runs before runcmd. The agent is NOT
	// enabled at boot via OpenRC — cloud-init is the sole trigger.
	ud := albVMUserData("lb-test")
	assert.Contains(t, ud, "write_files:")
	assert.Contains(t, ud, "/etc/conf.d/alb-agent")
	assert.Contains(t, ud, "rc-service")
	// Must not invoke the binary directly — OpenRC manages the process.
	assert.NotContains(t, ud, "/usr/local/bin/alb-agent")

	// write_files must appear before runcmd in the output
	wfIdx := strings.Index(ud, "write_files:")
	rcIdx := strings.Index(ud, "runcmd:")
	assert.Greater(t, rcIdx, wfIdx, "write_files must precede runcmd")
}

func TestAgentURL(t *testing.T) {
	assert.Equal(t, "http://10.0.1.5:8405", agentURL("10.0.1.5"))
}
