//go:build e2e

package scenarios

import "testing"

// TestScenarioB_DaemonRestartWithoutNATS — kill spinifex-nats, restart
// spinifex-daemon, verify the daemon starts within 30s (not the old
// 5-minute NATS-wait abort) and recovers its instances from the local
// state file. See
// docs/development/improvements/ddil-e2e-test-harness.md §3 Scenario B.
func TestScenarioB_DaemonRestartWithoutNATS(t *testing.T) {
	scenarioSkip(t, "B", "daemon-local-autonomy §1a/1d")
}
