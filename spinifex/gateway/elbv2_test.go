package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestELBv2ActionsMap_AllActionsRegistered(t *testing.T) {
	expectedActions := []string{
		"CreateLoadBalancer",
		"DeleteLoadBalancer",
		"DescribeLoadBalancers",
		"CreateTargetGroup",
		"DeleteTargetGroup",
		"DescribeTargetGroups",
		"RegisterTargets",
		"DeregisterTargets",
		"DescribeTargetHealth",
		"CreateListener",
		"DeleteListener",
		"DescribeListeners",
		"LBAgentHeartbeat",
		"GetLBConfig",
		"ModifyTargetGroupAttributes",
		"DescribeTargetGroupAttributes",
		"ModifyLoadBalancerAttributes",
		"DescribeLoadBalancerAttributes",
	}

	for _, action := range expectedActions {
		_, ok := elbv2Actions[action]
		assert.True(t, ok, "action %q should be registered in elbv2Actions", action)
	}

	assert.Len(t, elbv2Actions, len(expectedActions), "elbv2Actions should have exactly %d actions", len(expectedActions))
}

func TestELBv2ActionsMap_UnknownActionNotRegistered(t *testing.T) {
	unknownActions := []string{
		"ModifyLoadBalancer",
		"CreateRule",
		"DescribeRules",
		"RunInstances",
	}

	for _, action := range unknownActions {
		_, ok := elbv2Actions[action]
		assert.False(t, ok, "action %q should not be in elbv2Actions", action)
	}
}
