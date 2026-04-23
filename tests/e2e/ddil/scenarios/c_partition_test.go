//go:build e2e

package scenarios

import "testing"

// TestScenarioC_CleanPartition — iptables-DROP node3 away from node1 and
// node2, verify the majority keeps serving API, the isolated node
// reports standalone mode, and heal converges state without duplicate
// or orphaned VMs. See
// docs/development/improvements/ddil-e2e-test-harness.md §3 Scenario C.
func TestScenarioC_CleanPartition(t *testing.T) {
	scenarioSkip(t, "C", "daemon-local-autonomy §1a-1e")
}
