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

// OVNHealthStatus reports the readiness of OVN networking on this compute node.
type OVNHealthStatus struct {
	BrIntExists     bool   `json:"br_int_exists"`
	OVNControllerUp bool   `json:"ovn_controller_up"`
	ChassisID       string `json:"chassis_id,omitempty"`
	EncapIP         string `json:"encap_ip,omitempty"`
	OVNRemote       string `json:"ovn_remote,omitempty"`
}

// CheckOVNHealth probes local OVS/OVN state to determine network readiness.
func CheckOVNHealth() OVNHealthStatus {
	status := OVNHealthStatus{}

	// Check br-int exists
	if err := exec.Command("ovs-vsctl", "br-exists", "br-int").Run(); err == nil {
		status.BrIntExists = true
	}

	// Check ovn-controller is running via ovs-appctl (more reliable than pgrep)
	if out, err := exec.Command("ovs-appctl", "-t", "ovn-controller", "version").CombinedOutput(); err == nil && len(out) > 0 {
		status.OVNControllerUp = true
	}

	// Read chassis identity from OVS external_ids
	if out, err := exec.Command("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:system-id").CombinedOutput(); err == nil {
		status.ChassisID = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}
	if out, err := exec.Command("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:ovn-encap-ip").CombinedOutput(); err == nil {
		status.EncapIP = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}
	if out, err := exec.Command("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:ovn-remote").CombinedOutput(); err == nil {
		status.OVNRemote = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}

	return status
}

// SetupComputeNode configures OVS for OVN on this compute node.
// It creates br-int with secure fail-mode and sets the OVN external_ids.
func SetupComputeNode(chassisID, ovnRemote, encapIP string) error {
	// Create br-int if it doesn't exist
	if out, err := exec.Command("ovs-vsctl", "--may-exist", "add-br", "br-int").CombinedOutput(); err != nil {
		return fmt.Errorf("create br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Set fail-mode=secure (preserves flows during ovn-controller restart)
	if out, err := exec.Command("ovs-vsctl", "set", "Bridge", "br-int", "fail-mode=secure").CombinedOutput(); err != nil {
		return fmt.Errorf("set br-int fail-mode: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Disable in-band management (prevents OVS from adding its own flows)
	if out, err := exec.Command("ovs-vsctl", "set", "Bridge", "br-int", "other-config:disable-in-band=true").CombinedOutput(); err != nil {
		return fmt.Errorf("set br-int disable-in-band: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Bring br-int up
	if out, err := exec.Command("ip", "link", "set", "br-int", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("bring up br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Set OVN external_ids on the Open_vSwitch table
	if out, err := exec.Command("ovs-vsctl", "set", "Open_vSwitch", ".",
		fmt.Sprintf("external_ids:system-id=%s", chassisID),
		fmt.Sprintf("external_ids:ovn-remote=%s", ovnRemote),
		fmt.Sprintf("external_ids:ovn-encap-ip=%s", encapIP),
		"external_ids:ovn-encap-type=geneve",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("set OVN external_ids: %s: %w", strings.TrimSpace(string(out)), err)
	}

	slog.Info("OVN compute node configured",
		"chassis_id", chassisID,
		"ovn_remote", ovnRemote,
		"encap_ip", encapIP,
	)
	return nil
}
