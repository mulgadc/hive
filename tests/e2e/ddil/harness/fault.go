//go:build e2e

package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PartitionNode isolates target from peers by installing per-peer iptables
// DROP rules for both directions. The orchestrator's SSH source IP must not
// be one of peers (it normally isn't: the peers are cluster members, and the
// orchestrator is the CI runner). After applying, a sanity-check SSH echo
// confirms the control plane is still reachable; a lost control-plane SSH
// is a loud error rather than a silent hang.
//
// The rule set is additive (iptables -I INPUT/OUTPUT) so PartitionNode
// composes with any prior non-DDIL rules. HealNode's flush is still the
// correct teardown because the harness owns the target's firewall during a
// scenario.
func PartitionNode(ctx context.Context, ssh SSH, target Node, peers []Node) error {
	if len(peers) == 0 {
		return fmt.Errorf("ddil harness: PartitionNode %s: no peers supplied", target.Name)
	}

	var lines []string
	for _, p := range peers {
		lines = append(lines,
			fmt.Sprintf("sudo iptables -I INPUT -s %s -j DROP", shellQuote(p.Addr)),
			fmt.Sprintf("sudo iptables -I OUTPUT -d %s -j DROP", shellQuote(p.Addr)),
		)
	}
	cmd := strings.Join(lines, " && ")
	if _, err := ssh.Run(ctx, target, cmd); err != nil {
		return fmt.Errorf("ddil harness: partition %s: %w", target.Name, err)
	}

	// Sanity-check: if our SSH path ran through a peer IP, the rules we just
	// installed would have severed it. Fail loudly so a future infra change
	// (e.g. orchestrator moved onto the cluster network) surfaces immediately.
	if _, err := ssh.Run(ctx, target, "echo ok"); err != nil {
		return fmt.Errorf("ddil harness: partition %s severed orchestrator SSH "+
			"(orchestrator may share peer IPs): %w", target.Name, err)
	}
	return nil
}

// HealNode flushes and deletes all iptables rules on target, reversing a
// prior PartitionNode. Idempotent: safe on a node with no rules installed.
func HealNode(ctx context.Context, ssh SSH, target Node) error {
	if _, err := ssh.Run(ctx, target, "sudo iptables -F && sudo iptables -X"); err != nil {
		return fmt.Errorf("ddil harness: heal %s: %w", target.Name, err)
	}
	return nil
}

// KillNATS stops spinifex-nats on node without touching spinifex-daemon.
// This is the scenario shape the current happy-path suite cannot express —
// `systemctl stop spinifex.target` brings both down together.
func KillNATS(ctx context.Context, ssh SSH, node Node) error {
	if _, err := ssh.Run(ctx, node, "sudo systemctl stop spinifex-nats"); err != nil {
		return fmt.Errorf("ddil harness: kill nats on %s: %w", node.Name, err)
	}
	return nil
}

// StartNATS starts spinifex-nats on node. Paired with KillNATS.
func StartNATS(ctx context.Context, ssh SSH, node Node) error {
	if _, err := ssh.Run(ctx, node, "sudo systemctl start spinifex-nats"); err != nil {
		return fmt.Errorf("ddil harness: start nats on %s: %w", node.Name, err)
	}
	return nil
}

// RestartDaemonOnly restarts spinifex-daemon without touching spinifex-nats.
// Used by Scenario B to exercise the daemon-without-NATS startup path
// (daemon-local-autonomy §1d).
func RestartDaemonOnly(ctx context.Context, ssh SSH, node Node) error {
	if _, err := ssh.Run(ctx, node, "sudo systemctl restart spinifex-daemon"); err != nil {
		return fmt.Errorf("ddil harness: restart daemon on %s: %w", node.Name, err)
	}
	return nil
}

// WaitForMode polls the daemon's /local/status until it reports the expected
// mode or timeout expires. Poll interval is 1s — fast enough that a
// 30-second timeout sees ~30 attempts, slow enough not to flood a recovering
// daemon.
//
// Depends on daemon-local-autonomy §1b. Until that endpoint ships, this
// function will time out; callers gate on t.Skip.
func WaitForMode(ctx context.Context, dc *DaemonClient, node Node, want DaemonMode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	const interval = 1 * time.Second

	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status, err := dc.Status(ctx, node)
		if err == nil && status.Mode == want {
			return nil
		}
		lastErr = err

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("ddil harness: wait for mode %s on %s: timed out after %s: last error: %w",
					want, node.Name, timeout, lastErr)
			}
			return fmt.Errorf("ddil harness: wait for mode %s on %s: timed out after %s (still reporting another mode)",
				want, node.Name, timeout)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
