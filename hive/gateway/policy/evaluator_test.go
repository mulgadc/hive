package policy

import (
	"testing"

	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
)

// helper to build a single-statement policy document.
func doc(effect, action, resource string) handlers_iam.PolicyDocument {
	return handlers_iam.PolicyDocument{
		Version: "2012-10-17",
		Statement: []handlers_iam.Statement{
			{Effect: effect, Action: handlers_iam.StringOrArr{action}, Resource: handlers_iam.StringOrArr{resource}},
		},
	}
}

func TestEvaluateAccess_RootBypass(t *testing.T) {
	// Root user is always allowed, even with no policies.
	got := EvaluateAccess("root", "ec2:TerminateInstances", "*", nil)
	if got != Allow {
		t.Fatalf("expected Allow for root, got %v", got)
	}
}

func TestEvaluateAccess_RootBypassWithExplicitDeny(t *testing.T) {
	// Root bypasses even an explicit Deny.
	policies := []handlers_iam.PolicyDocument{
		doc("Deny", "*", "*"),
	}
	got := EvaluateAccess("root", "ec2:TerminateInstances", "*", policies)
	if got != Allow {
		t.Fatalf("expected Allow for root even with explicit deny, got %v", got)
	}
}

func TestEvaluateAccess_DefaultDeny(t *testing.T) {
	// Non-root with no policies → default deny.
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", nil)
	if got != Deny {
		t.Fatalf("expected default Deny with no policies, got %v", got)
	}
}

func TestEvaluateAccess_DefaultDenyEmptyPolicies(t *testing.T) {
	// Non-root with empty policy list → default deny.
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", []handlers_iam.PolicyDocument{})
	if got != Deny {
		t.Fatalf("expected default Deny with empty policies, got %v", got)
	}
}

func TestEvaluateAccess_ExplicitAllow(t *testing.T) {
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "ec2:RunInstances", "*"),
	}
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", policies)
	if got != Allow {
		t.Fatalf("expected Allow, got %v", got)
	}
}

func TestEvaluateAccess_ExplicitDenyWins(t *testing.T) {
	// Explicit deny overrides an explicit allow.
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "ec2:*", "*"),
		doc("Deny", "ec2:TerminateInstances", "*"),
	}
	got := EvaluateAccess("alice", "ec2:TerminateInstances", "*", policies)
	if got != Deny {
		t.Fatalf("expected Deny (explicit deny wins), got %v", got)
	}
}

func TestEvaluateAccess_ExplicitDenyWinsSamePolicy(t *testing.T) {
	// Deny and Allow in the same policy document — Deny wins.
	policies := []handlers_iam.PolicyDocument{
		{
			Version: "2012-10-17",
			Statement: []handlers_iam.Statement{
				{Effect: "Allow", Action: handlers_iam.StringOrArr{"ec2:*"}, Resource: handlers_iam.StringOrArr{"*"}},
				{Effect: "Deny", Action: handlers_iam.StringOrArr{"ec2:TerminateInstances"}, Resource: handlers_iam.StringOrArr{"*"}},
			},
		},
	}
	got := EvaluateAccess("alice", "ec2:TerminateInstances", "*", policies)
	if got != Deny {
		t.Fatalf("expected Deny (same-policy explicit deny), got %v", got)
	}
}

func TestEvaluateAccess_NoMatchingAction(t *testing.T) {
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "s3:GetObject", "*"),
	}
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", policies)
	if got != Deny {
		t.Fatalf("expected Deny (no matching action), got %v", got)
	}
}

func TestEvaluateAccess_WildcardAllActions(t *testing.T) {
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "*", "*"),
	}
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", policies)
	if got != Allow {
		t.Fatalf("expected Allow with wildcard *, got %v", got)
	}
}

func TestEvaluateAccess_ServiceWildcard(t *testing.T) {
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "ec2:*", "*"),
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"ec2:RunInstances", Allow},
		{"ec2:DescribeInstances", Allow},
		{"s3:GetObject", Deny},
		{"iam:CreateUser", Deny},
	}

	for _, tt := range tests {
		got := EvaluateAccess("alice", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("ec2:* policy, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

func TestEvaluateAccess_PrefixWildcard(t *testing.T) {
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "s3:Get*", "*"),
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"s3:GetObject", Allow},
		{"s3:GetBucketPolicy", Allow},
		{"s3:PutObject", Deny},
		{"s3:DeleteObject", Deny},
	}

	for _, tt := range tests {
		got := EvaluateAccess("alice", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("s3:Get* policy, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

func TestEvaluateAccess_MultipleActions(t *testing.T) {
	// A statement with multiple actions.
	policies := []handlers_iam.PolicyDocument{
		{
			Version: "2012-10-17",
			Statement: []handlers_iam.Statement{
				{
					Effect:   "Allow",
					Action:   handlers_iam.StringOrArr{"ec2:DescribeInstances", "ec2:RunInstances"},
					Resource: handlers_iam.StringOrArr{"*"},
				},
			},
		},
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"ec2:DescribeInstances", Allow},
		{"ec2:RunInstances", Allow},
		{"ec2:TerminateInstances", Deny},
	}

	for _, tt := range tests {
		got := EvaluateAccess("alice", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("multi-action policy, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

func TestEvaluateAccess_MultiplePolicies(t *testing.T) {
	// Permissions spread across multiple policy documents.
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "ec2:DescribeInstances", "*"),
		doc("Allow", "s3:GetObject", "*"),
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"ec2:DescribeInstances", Allow},
		{"s3:GetObject", Allow},
		{"iam:CreateUser", Deny},
	}

	for _, tt := range tests {
		got := EvaluateAccess("alice", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("multi-policy, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

func TestEvaluateAccess_ResourceMismatch(t *testing.T) {
	// Allow only on a specific resource, request uses "*".
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "s3:GetObject", "arn:aws:s3:::my-bucket/*"),
	}
	got := EvaluateAccess("alice", "s3:GetObject", "*", policies)
	if got != Deny {
		t.Fatalf("expected Deny (resource mismatch), got %v", got)
	}
}

func TestEvaluateAccess_CaseInsensitiveAction(t *testing.T) {
	// Action matching should be case-insensitive for exact matches.
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "EC2:RunInstances", "*"),
	}
	got := EvaluateAccess("alice", "ec2:RunInstances", "*", policies)
	if got != Allow {
		t.Fatalf("expected Allow (case-insensitive match), got %v", got)
	}
}

// --- matchWildcard tests ---

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		// Global wildcard
		{"*", "anything", true},
		{"*", "", true},

		// Service wildcard
		{"ec2:*", "ec2:RunInstances", true},
		{"ec2:*", "ec2:DescribeInstances", true},
		{"ec2:*", "s3:GetObject", false},

		// Prefix wildcard
		{"s3:Get*", "s3:GetObject", true},
		{"s3:Get*", "s3:GetBucketPolicy", true},
		{"s3:Get*", "s3:PutObject", false},

		// Exact match
		{"ec2:RunInstances", "ec2:RunInstances", true},
		{"ec2:RunInstances", "ec2:StopInstances", false},

		// Case insensitive exact match
		{"EC2:RunInstances", "ec2:RunInstances", true},
		{"ec2:runinstances", "ec2:RunInstances", true},

		// Edge cases
		{"", "", true},
		{"", "something", false},
	}

	for _, tt := range tests {
		got := matchWildcard(tt.pattern, tt.value)
		if got != tt.want {
			t.Errorf("matchWildcard(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

// --- Action mapping tests ---

func TestIAMAction(t *testing.T) {
	got := IAMAction("ec2", "RunInstances")
	if got != "ec2:RunInstances" {
		t.Fatalf("IAMAction(ec2, RunInstances) = %q, want %q", got, "ec2:RunInstances")
	}
}

func TestLookupAction(t *testing.T) {
	tests := []struct {
		service string
		action  string
		want    string
		wantOK  bool
	}{
		{"ec2", "RunInstances", "ec2:RunInstances", true},
		{"ec2", "DescribeInstances", "ec2:DescribeInstances", true},
		{"iam", "CreateUser", "iam:CreateUser", true},
		{"iam", "CreatePolicy", "iam:CreatePolicy", true},
		{"ec2", "NonExistentAction", "", false},
		{"s3", "GetObject", "", false}, // S3 not mapped yet
		{"unknown", "Foo", "", false},
	}

	for _, tt := range tests {
		got, ok := LookupAction(tt.service, tt.action)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("LookupAction(%q, %q) = (%q, %v), want (%q, %v)",
				tt.service, tt.action, got, ok, tt.want, tt.wantOK)
		}
	}
}

// --- Realistic scenario tests ---

func TestEvaluateAccess_ReadOnlyUser(t *testing.T) {
	// Simulate a read-only user: allow all Describe* actions, deny everything else.
	policies := []handlers_iam.PolicyDocument{
		{
			Version: "2012-10-17",
			Statement: []handlers_iam.Statement{
				{
					Effect:   "Allow",
					Action:   handlers_iam.StringOrArr{"ec2:Describe*"},
					Resource: handlers_iam.StringOrArr{"*"},
				},
			},
		},
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"ec2:DescribeInstances", Allow},
		{"ec2:DescribeVolumes", Allow},
		{"ec2:DescribeVpcs", Allow},
		{"ec2:RunInstances", Deny},
		{"ec2:TerminateInstances", Deny},
		{"iam:CreateUser", Deny},
	}

	for _, tt := range tests {
		got := EvaluateAccess("viewer", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("read-only user, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

func TestEvaluateAccess_AdminWithDenyTerminate(t *testing.T) {
	// Admin that can do everything except terminate instances.
	policies := []handlers_iam.PolicyDocument{
		doc("Allow", "*", "*"),
		doc("Deny", "ec2:TerminateInstances", "*"),
	}

	tests := []struct {
		action string
		want   Decision
	}{
		{"ec2:RunInstances", Allow},
		{"ec2:DescribeInstances", Allow},
		{"s3:GetObject", Allow},
		{"iam:CreateUser", Allow},
		{"ec2:TerminateInstances", Deny}, // explicit deny
	}

	for _, tt := range tests {
		got := EvaluateAccess("admin", tt.action, "*", policies)
		if got != tt.want {
			t.Errorf("admin-no-terminate, action=%s: expected %v, got %v", tt.action, tt.want, got)
		}
	}
}

// TestEC2ActionsComplete verifies every EC2 action in the map has the correct format.
func TestEC2ActionsComplete(t *testing.T) {
	for action, iamAction := range EC2Actions {
		expected := "ec2:" + action
		if iamAction != expected {
			t.Errorf("EC2Actions[%q] = %q, want %q", action, iamAction, expected)
		}
	}
}

// TestIAMActionsComplete verifies every IAM action in the map has the correct format.
func TestIAMActionsComplete(t *testing.T) {
	for action, iamAction := range IAMActions {
		expected := "iam:" + action
		if iamAction != expected {
			t.Errorf("IAMActions[%q] = %q, want %q", action, iamAction, expected)
		}
	}
}
