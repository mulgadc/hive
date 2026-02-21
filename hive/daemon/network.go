package daemon

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// NetworkPlumber handles tap device and OVS bridge operations for VM networking.
// The live implementation runs system commands (ip, ovs-vsctl); tests use a mock.
type NetworkPlumber interface {
	// SetupTapDevice creates a tap device and adds it to the OVS br-int bridge
	// with the correct iface-id for OVN port binding.
	SetupTapDevice(eniId, mac string) error

	// CleanupTapDevice removes the tap device from br-int and deletes it.
	CleanupTapDevice(eniId string) error
}

// OVSNetworkPlumber implements NetworkPlumber using system commands.
type OVSNetworkPlumber struct{}

func (p *OVSNetworkPlumber) SetupTapDevice(eniId, mac string) error {
	tapName := TapDeviceName(eniId)
	ifaceID := OVSIfaceID(eniId)

	// 1. Create tap device
	if out, err := exec.Command("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("create tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 2. Bring tap up
	if out, err := exec.Command("ip", "link", "set", tapName, "up").CombinedOutput(); err != nil {
		_ = exec.Command("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return fmt.Errorf("bring up tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 3. Add to br-int with iface-id for OVN port binding
	if out, err := exec.Command("ovs-vsctl",
		"add-port", "br-int", tapName,
		"--", "set", "Interface", tapName,
		fmt.Sprintf("external_ids:iface-id=%s", ifaceID),
		fmt.Sprintf("external_ids:attached-mac=%s", mac),
	).CombinedOutput(); err != nil {
		_ = exec.Command("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return fmt.Errorf("add tap to br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	slog.Info("Network plumbing complete", "tap", tapName, "iface-id", ifaceID, "mac", mac)
	return nil
}

func (p *OVSNetworkPlumber) CleanupTapDevice(eniId string) error {
	tapName := TapDeviceName(eniId)

	// 1. Remove from br-int (--if-exists avoids error if already gone)
	if out, err := exec.Command("ovs-vsctl", "--if-exists", "del-port", "br-int", tapName).CombinedOutput(); err != nil {
		slog.Warn("Failed to remove tap from br-int", "tap", tapName, "err", err, "out", strings.TrimSpace(string(out)))
	}

	// 2. Delete tap device
	if out, err := exec.Command("ip", "tuntap", "del", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("delete tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	slog.Info("Network cleanup complete", "tap", tapName)
	return nil
}

// TapDeviceName returns the Linux tap device name for an ENI.
// Linux IFNAMSIZ limits interface names to 15 characters.
// ENI IDs like "eni-abc123def456789" are too long, so we truncate.
func TapDeviceName(eniId string) string {
	id := strings.TrimPrefix(eniId, "eni-")
	name := "tap" + id
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

// OVSIfaceID returns the OVS external_ids:iface-id value for an ENI.
// This must match the OVN LogicalSwitchPort name for ovn-controller binding.
func OVSIfaceID(eniId string) string {
	return "port-" + eniId
}
