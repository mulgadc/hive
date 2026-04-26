//go:build e2e

package harness

import (
	"context"
	"fmt"
	"strings"
)

// ApplyNetem installs a tc netem qdisc on iface of node matching profile.
// Idempotent: any existing root qdisc on the interface is replaced.
//
// Flapping profiles have no static tc expression — they are driven by a
// separate orchestrator (not yet implemented). ApplyNetem rejects them with
// a clear error so callers don't silently apply a no-op.
func ApplyNetem(ctx context.Context, ssh SSH, node Node, iface string, profile LinkProfile) error {
	if profile.Flapping != nil {
		return fmt.Errorf("ddil harness: profile %q is flap-driven; use a flap orchestrator, not ApplyNetem", profile.Name)
	}

	var netemArgs []string
	if profile.Delay > 0 {
		delay := fmt.Sprintf("%dms", profile.Delay.Milliseconds())
		if profile.Jitter > 0 {
			delay += fmt.Sprintf(" %dms distribution normal", profile.Jitter.Milliseconds())
		}
		netemArgs = append(netemArgs, "delay", delay)
	}
	if profile.Loss > 0 {
		netemArgs = append(netemArgs, "loss", fmt.Sprintf("%.3f%%", profile.Loss))
	}
	if profile.Bandwidth != "" {
		netemArgs = append(netemArgs, "rate", profile.Bandwidth)
	}
	if len(netemArgs) == 0 {
		return fmt.Errorf("ddil harness: profile %q has no netem parameters", profile.Name)
	}

	// `tc qdisc replace` is idempotent — it adds if absent, replaces if
	// present — which matches ApplyNetem's contract.
	cmd := fmt.Sprintf("sudo tc qdisc replace dev %s root netem %s",
		shellQuote(iface), strings.Join(netemArgs, " "))
	if _, err := ssh.Run(ctx, node, cmd); err != nil {
		return fmt.Errorf("ddil harness: apply netem %s on %s (%s): %w",
			profile.Name, node.Name, iface, err)
	}
	return nil
}

// ClearNetem removes any root qdisc on iface. Safe to call when no qdisc is
// present — tc's non-zero exit is swallowed on the remote side.
func ClearNetem(ctx context.Context, ssh SSH, node Node, iface string) error {
	cmd := fmt.Sprintf("sudo tc qdisc del dev %s root 2>/dev/null || true", shellQuote(iface))
	if _, err := ssh.Run(ctx, node, cmd); err != nil {
		return fmt.Errorf("ddil harness: clear netem on %s (%s): %w", node.Name, iface, err)
	}
	return nil
}

// shellQuote wraps s in single quotes and escapes embedded single quotes, so
// that shell metacharacters in interface names or peer addresses are treated
// as literals on the remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
