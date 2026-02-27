// Package policy implements IAM policy evaluation for access control decisions.
package policy

import (
	"log/slog"
	"strings"

	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
)

// Decision represents the outcome of a policy evaluation.
type Decision int

const (
	// Deny is the default — no matching Allow, or an explicit Deny.
	Deny Decision = iota
	// Allow means an explicit Allow was found with no overriding Deny.
	Allow
)

// EvaluateAccess checks whether the given identity is permitted to perform
// the specified action on the specified resource, based on the supplied
// policy documents. It follows AWS's evaluation order:
//
//  1. Root user → always Allow (bypass evaluation entirely).
//  2. Explicit Deny in any statement → Deny (wins immediately).
//  3. Explicit Allow in any statement → Allow.
//  4. No matching statement → Deny (implicit default).
func EvaluateAccess(identity, action, resource string, policies []handlers_iam.PolicyDocument) Decision {
	if identity == "root" {
		return Allow
	}

	hasAllow := false
	for i := range policies {
		for j := range policies[i].Statement {
			stmt := &policies[i].Statement[j]

			if !matchesAny(stmt.Action, action) {
				continue
			}
			if !matchesAny(stmt.Resource, resource) {
				continue
			}
			switch stmt.Effect {
			case "Deny":
				return Deny
			case "Allow":
				hasAllow = true
			default:
				slog.Warn("EvaluateAccess: unrecognized Effect, treating as Deny",
					"effect", stmt.Effect, "action", action, "identity", identity)
				return Deny
			}
		}
	}

	if hasAllow {
		return Allow
	}
	return Deny
}

// matchesAny returns true if any pattern in patterns matches the given value.
// Supports the same wildcard matching for both actions and resources:
//   - "*"                — matches everything
//   - "ec2:*"            — matches all actions in the ec2 service
//   - "s3:Get*"          — matches s3:GetObject, s3:GetBucketPolicy, etc.
//   - "ec2:RunInstances" — exact match
func matchesAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if matchWildcard(p, value) {
			return true
		}
	}
	return false
}

// matchWildcard performs simple wildcard matching where "*" can appear at the
// end of a pattern as a suffix wildcard, or alone to match everything.
// Examples:
//
//	"*"              matches anything
//	"ec2:*"          matches "ec2:RunInstances"
//	"s3:Get*"        matches "s3:GetObject"
//	"ec2:RunInstances" matches only "ec2:RunInstances"
func matchWildcard(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(value, prefix)
	}
	return strings.EqualFold(pattern, value)
}
