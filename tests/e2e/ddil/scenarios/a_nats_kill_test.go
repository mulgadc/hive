//go:build e2e

package scenarios

import "testing"

// TestScenarioA_NATSKill — stop spinifex-nats on a single node without
// touching spinifex-daemon, verify the daemon stays up in standalone
// mode, serves its local API, and the surviving 2-node quorum keeps
// serving cluster-wide requests. See
// docs/development/improvements/ddil-e2e-test-harness.md §3 Scenario A.
func TestScenarioA_NATSKill(t *testing.T) {
	scenarioSkip(t, "A", "daemon-local-autonomy §1a/1b/1c")
}
