package handlers_ec2_vpc

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

const dhcpcdBin = "/usr/sbin/dhcpcd"

// ObtainDHCPLease requests a DHCP lease from the router on the given OVS bridge
// using a unique client ID. Returns the leased IP. The lease is obtained with
// --noconfigure so the IP is NOT added to the bridge interface — OVN handles
// traffic via its NAT rules.
func ObtainDHCPLease(bridge, clientID string) (string, error) {
	if bridge == "" {
		return "", fmt.Errorf("DHCP lease: bridge name is required")
	}
	if clientID == "" {
		return "", fmt.Errorf("DHCP lease: client ID is required")
	}

	// Run dhcpcd with:
	//   --noconfigure   = don't add IP to interface (OVN handles traffic)
	//   -1              = exit after obtaining a lease (don't daemonize)
	//   -4              = IPv4 only
	//   -I clientID     = unique client identifier per ENI
	//   -t 15           = 15 second timeout
	cmd := exec.Command("sudo", dhcpcdBin,
		"--noconfigure",
		"-1",
		"-4",
		"-I", clientID,
		"-t", "15",
		bridge,
	)
	output, err := cmd.CombinedOutput()
	slog.Debug("dhcpcd output", "bridge", bridge, "clientID", clientID, "output", string(output), "err", err)

	// Parse the leased IP from dhcpcd output.
	// dhcpcd prints: "br-ext: leased 192.168.1.75 for 1800 seconds"
	// Note: dhcpcd may exit non-zero with --noconfigure even after obtaining
	// a lease (it reports "timed out" waiting for interface configuration).
	// We check for a lease in the output regardless of exit code.
	ip := parseDHCPCDLeasedIP(string(output))
	if ip == "" {
		if err != nil {
			return "", fmt.Errorf("dhcpcd failed on %s (client %s): %w\noutput: %s", bridge, clientID, err, string(output))
		}
		return "", fmt.Errorf("dhcpcd produced no lease on %s (client %s): %s", bridge, clientID, string(output))
	}

	slog.Info("DHCP lease obtained", "bridge", bridge, "clientID", clientID, "ip", ip)
	return ip, nil
}

// ReleaseDHCPLease releases a previously obtained DHCP lease.
func ReleaseDHCPLease(bridge, clientID string) error {
	if bridge == "" || clientID == "" {
		return nil
	}

	cmd := exec.Command("sudo", dhcpcdBin,
		"--release",
		"-4",
		"-I", clientID,
		bridge,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("dhcpcd release", "bridge", bridge, "clientID", clientID, "output", string(output), "err", err)
		// Not fatal — lease will expire naturally
		return fmt.Errorf("dhcpcd release failed on %s (client %s): %w", bridge, clientID, err)
	}

	slog.Info("DHCP lease released", "bridge", bridge, "clientID", clientID)
	return nil
}

// parseDHCPCDLeasedIP extracts the leased IP from dhcpcd stdout.
// Looks for: "<iface>: leased <IP> for <N> seconds"
func parseDHCPCDLeasedIP(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if before, after, found := strings.Cut(line, ": leased "); found && before != "" {
			if spaceIdx := strings.IndexByte(after, ' '); spaceIdx > 0 {
				return after[:spaceIdx]
			}
		}
	}
	return ""
}
