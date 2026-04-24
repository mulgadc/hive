//go:build e2e

package harness

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run wraps a scenario with reset-and-retry plus quarantine handling. It is
// the primary entry point used by the files in scenarios/:
//
//	harness.Run(t, cluster, ssh, "A", func(t *testing.T) { ... })
//
// Quarantine: scenarios whose letter appears in DDIL_QUARANTINED are skipped
// instead of executed. Per the design doc, a quarantined scenario must have
// a linked bead tracking its fix; the live status is tracked in
// tests/e2e/TEST_COVERAGE.md.
//
// Retry: a first-attempt failure runs ResetAllNodes and invokes fn once more
// as a fresh subtest. Both attempts appear in the test output so CI logs
// preserve the transient-vs-deterministic signal. The first-attempt failure
// is not hidden — Go's testing package propagates subtest failures to the
// parent — which is intentional: the retry is a diagnostic aid, not a way
// to mask flakes. Scenarios crossing the 5% nightly flake threshold are
// moved to DDIL_QUARANTINED instead.
func Run(t *testing.T, c *Cluster, ssh SSH, letter string, fn func(*testing.T)) {
	t.Helper()

	if IsQuarantined(letter) {
		t.Skipf("QUARANTINED: scenario %s (DDIL_QUARANTINED=%s)", letter, os.Getenv("DDIL_QUARANTINED"))
		return
	}

	if t.Run("attempt-1", fn) {
		return
	}

	t.Logf("ddil harness: scenario %s failed first attempt, resetting cluster and retrying", letter)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ResetAllNodes(ctx, c, ssh); err != nil {
		t.Errorf("ddil harness: reset before retry of %s: %v", letter, err)
		return
	}
	t.Run("attempt-2", fn)
}

// IsQuarantined reports whether letter appears in DDIL_QUARANTINED.
// Comparison is case-insensitive; whitespace and empty entries are tolerated
// so the env var can be edited by humans without worrying about formatting.
func IsQuarantined(letter string) bool {
	raw := os.Getenv("DDIL_QUARANTINED")
	if raw == "" {
		return false
	}
	want := strings.ToUpper(strings.TrimSpace(letter))
	for _, part := range strings.Split(raw, ",") {
		if strings.ToUpper(strings.TrimSpace(part)) == want {
			return true
		}
	}
	return false
}
