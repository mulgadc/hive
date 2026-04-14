package handlers_elbv2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildLBArn(t *testing.T) {
	arn := buildLBArn("us-east-1", "123456789012", "my-alb", "50dc6c495c0c9188", LoadBalancerTypeApplication)
	assert.Equal(t, "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/50dc6c495c0c9188", arn)
}

func TestBuildLBArn_Network(t *testing.T) {
	arn := buildLBArn("us-east-1", "123456789012", "my-nlb", "50dc6c495c0c9188", LoadBalancerTypeNetwork)
	assert.Equal(t, "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/net/my-nlb/50dc6c495c0c9188", arn)
}

func TestBuildTGArn(t *testing.T) {
	arn := buildTGArn("us-west-2", "111222333444", "my-tg", "deadbeef")
	assert.Equal(t, "arn:aws:elasticloadbalancing:us-west-2:111222333444:targetgroup/my-tg/deadbeef", arn)
}

func TestBuildListenerArn(t *testing.T) {
	arn := buildListenerArn("eu-west-1", "999888777666", "my-alb", "lbid123", "listener456", LoadBalancerTypeApplication)
	assert.Equal(t, "arn:aws:elasticloadbalancing:eu-west-1:999888777666:listener/app/my-alb/lbid123/listener456", arn)
}

func TestBuildListenerArn_NLB(t *testing.T) {
	arn := buildListenerArn("eu-west-1", "999888777666", "my-nlb", "lbid123", "listener456", LoadBalancerTypeNetwork)
	assert.Equal(t, "arn:aws:elasticloadbalancing:eu-west-1:999888777666:listener/net/my-nlb/lbid123/listener456", arn)
}

func TestLbVMUserData_ContainsLBID(t *testing.T) {
	svc := &ELBv2ServiceImpl{
		GatewayURL:      "https://192.168.1.33:9999",
		SystemAccessKey: "AKIAIOSFODNN7EXAMPLE",
		SystemSecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		region:          "ap-southeast-2",
	}
	ud, err := svc.lbVMUserData("lb-abc123")
	require.NoError(t, err)
	assert.Contains(t, ud, "#cloud-config")
	assert.Contains(t, ud, "write_files:")
	assert.Contains(t, ud, "/etc/conf.d/lb-agent")
	assert.Contains(t, ud, "LB_LB_ID=lb-abc123")
	assert.Contains(t, ud, "LB_GATEWAY_URL=https://192.168.1.33:9999")
	assert.Contains(t, ud, "LB_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, ud, "LB_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	assert.Contains(t, ud, "LB_REGION=ap-southeast-2")
	// NATS URL should no longer be present
	assert.NotContains(t, ud, "NATS")
}

func TestLbVMUserData_WriteFilesThenRuncmd(t *testing.T) {
	// Cloud-init guarantees write_files runs before runcmd. The agent is NOT
	// enabled at boot via OpenRC — cloud-init is the sole trigger.
	svc := &ELBv2ServiceImpl{
		GatewayURL:      "https://10.0.2.2:9999",
		SystemAccessKey: "AKID",
		SystemSecretKey: "SECRET",
	}
	ud, err := svc.lbVMUserData("lb-test")
	require.NoError(t, err)
	assert.Contains(t, ud, "write_files:")
	assert.Contains(t, ud, "/etc/conf.d/lb-agent")
	assert.Contains(t, ud, "rc-service")
	// Must not invoke the binary directly — OpenRC manages the process.
	assert.NotContains(t, ud, "/usr/local/bin/lb-agent")

	// write_files must appear before runcmd in the output
	wfIdx := strings.Index(ud, "write_files:")
	rcIdx := strings.Index(ud, "runcmd:")
	assert.Greater(t, rcIdx, wfIdx, "write_files must precede runcmd")
}

func TestLbVMUserData_NoCACert(t *testing.T) {
	// CA cert is injected by the instance service's cloud-init template,
	// not by lbVMUserData — verify it's not duplicated here.
	svc := &ELBv2ServiceImpl{
		GatewayURL:      "https://192.168.1.33:9999",
		SystemAccessKey: "AKID",
		SystemSecretKey: "SECRET",
	}
	ud, err := svc.lbVMUserData("lb-noca")
	require.NoError(t, err)
	assert.NotContains(t, ud, "ca_certs:")
}

func TestLbVMUserData_EmptyGatewayURL(t *testing.T) {
	svc := &ELBv2ServiceImpl{
		SystemAccessKey: "AKID",
		SystemSecretKey: "SECRET",
	}
	_, err := svc.lbVMUserData("lb-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing system credentials")
}

func TestLbVMUserData_EmptyAccessKey(t *testing.T) {
	svc := &ELBv2ServiceImpl{
		GatewayURL:      "https://10.0.0.1:9999",
		SystemSecretKey: "SECRET",
	}
	_, err := svc.lbVMUserData("lb-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing system credentials")
}

func TestLbVMUserData_EmptySecretKey(t *testing.T) {
	svc := &ELBv2ServiceImpl{
		GatewayURL:      "https://10.0.0.1:9999",
		SystemAccessKey: "AKID",
	}
	_, err := svc.lbVMUserData("lb-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing system credentials")
}
