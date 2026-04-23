// Package ddil is the root of the DDIL (Denied, Disrupted, Intermittent,
// Limited) E2E test harness.
//
// See docs/development/improvements/ddil-e2e-test-harness.md in the mulga
// monorepo for the full design.
//
// Sub-packages:
//   - harness: typed primitives (cluster, SSH, fault injection, witness VMs,
//     snapshot/state assertions, cleanup/retry, daemon client).
//   - scenarios: test files (build tag e2e) that exercise the hardening
//     epics via the harness primitives.
//
// All non-scaffolding files in this tree are tagged //go:build e2e so that
// default go build ./... skips them.
package ddil
