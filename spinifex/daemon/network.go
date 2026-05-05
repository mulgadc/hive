package daemon

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/mulgadc/spinifex/spinifex/vm"
)

// sudoCommand wraps exec.Command with sudo when running as non-root.
// OVS/OVN and ip commands require elevated privileges; in Docker and
// production the daemon runs as root, but in dev environments it may not.
//
// Bound to a var (not a plain func) so tests can swap in a stub — running
// the live binary against the dev host's OVS would mutate `external_ids` on
// the running cluster (see TestSetupComputeNode_ValidatesArgs).
var sudoCommand = func(name string, args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command(name, args...)
	}
	return exec.Command("sudo", append([]string{name}, args...)...)
}

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

var (
	_ NetworkPlumber    = (*OVSNetworkPlumber)(nil)
	_ vm.NetworkPlumber = (*OVSNetworkPlumber)(nil)
)

func (p *OVSNetworkPlumber) SetupTapDevice(eniId, mac string) error {
	tapName := vm.TapDeviceName(eniId)
	ifaceID := OVSIfaceID(eniId)

	// 0. Clear any prior state on both the OVS and kernel sides. They have
	// independent persistence — OVS conf.db survives reboot, kernel taps
	// don't — so a /sys/class/net check alone misses orphan OVS ports left
	// over after a host reboot or a terminate-during-pending race. The
	// --if-exists flag makes del-port a no-op when the port is absent, so
	// unconditional invocation is safe. Reject --may-exist add-port: it
	// would silently keep stale external_ids:iface-id/attached-mac from a
	// prior launch with a different ENI.
	if err := sudoCommand("ovs-vsctl", "--if-exists", "del-port", "br-int", tapName).Run(); err != nil {
		slog.Warn("Pre-create del-port failed (continuing)", "tap", tapName, "err", err)
	}
	if _, err := os.Stat("/sys/class/net/" + tapName); err == nil {
		if err := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run(); err != nil {
			slog.Warn("Pre-create tap del failed (continuing)", "tap", tapName, "err", err)
		}
	}

	// 1. Create tap device
	if out, err := sudoCommand("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("create tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 2. Bring tap up
	if out, err := sudoCommand("ip", "link", "set", tapName, "up").CombinedOutput(); err != nil {
		if cleanErr := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run(); cleanErr != nil {
			slog.Warn("Failed to clean up tap device after bring-up failure", "tap", tapName, "err", cleanErr)
		}
		return fmt.Errorf("bring up tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 3. Add to br-int with iface-id for OVN port binding
	if out, err := sudoCommand("ovs-vsctl",
		"add-port", "br-int", tapName,
		"--", "set", "Interface", tapName,
		fmt.Sprintf("external_ids:iface-id=%s", ifaceID),
		fmt.Sprintf("external_ids:attached-mac=%s", mac),
	).CombinedOutput(); err != nil {
		if cleanErr := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run(); cleanErr != nil {
			slog.Warn("Failed to clean up tap device after OVS failure", "tap", tapName, "err", cleanErr)
		}
		return fmt.Errorf("add tap to br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	slog.Info("Network plumbing complete", "tap", tapName, "iface-id", ifaceID, "mac", mac)
	return nil
}

func (p *OVSNetworkPlumber) CleanupTapDevice(eniId string) error {
	tapName := vm.TapDeviceName(eniId)

	// 1. Remove from br-int (--if-exists avoids error if already gone)
	if out, err := sudoCommand("ovs-vsctl", "--if-exists", "del-port", "br-int", tapName).CombinedOutput(); err != nil {
		slog.Warn("Failed to remove tap from br-int", "tap", tapName, "err", err, "out", strings.TrimSpace(string(out)))
	}

	// 2. Delete tap device
	if out, err := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("delete tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	slog.Info("Network cleanup complete", "tap", tapName)
	return nil
}

// cleanupExtraENITaps removes tap devices for every extra ENI attached to a
// system VM. Errors are logged but not returned so a partial cleanup still
// frees as many resources as possible.
func (d *Daemon) cleanupExtraENITaps(instance *vm.VM) {
	for _, extra := range instance.ExtraENIs {
		if err := d.networkPlumber.CleanupTapDevice(extra.ENIID); err != nil {
			slog.Warn("Failed to clean up extra ENI tap device", "eni", extra.ENIID, "err", err)
		}
	}
}

// OVSIfaceID returns the OVS external_ids:iface-id value for an ENI.
// This must match the OVN LogicalSwitchPort name for ovn-controller binding.
func OVSIfaceID(eniId string) string {
	return "port-" + eniId
}

// generateMgmtMAC creates a locally-administered unicast MAC for the
// management NIC. The "mgmt:" tag disambiguates from the dev NIC of the
// same instance (which shares instanceId).
func generateMgmtMAC(instanceId string) string {
	return utils.HashMAC("mgmt:" + instanceId)
}

// MgmtTapName returns the Linux TAP device name for a management NIC.
// Uses "mg" prefix + truncated instance ID to stay within 15-char IFNAMSIZ.
func MgmtTapName(instanceID string) string {
	id := strings.TrimPrefix(instanceID, "i-")
	name := "mg" + id
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

// GetBridgeIPv4 returns the first IPv4 address on the named Linux bridge.
// Returns "", nil if the bridge does not exist.
func GetBridgeIPv4(bridgeName string) (string, error) {
	iface, err := net.InterfaceByName(bridgeName)
	if err != nil {
		// "no such network interface" means the bridge doesn't exist yet — expected.
		// Other errors (permission denied on /sys/class/net/, etc.) are real failures.
		if strings.Contains(err.Error(), "no such network interface") {
			return "", nil
		}
		return "", fmt.Errorf("lookup %s: %w", bridgeName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("list addrs on %s: %w", bridgeName, err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.To4() != nil {
			return ipNet.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address on %s", bridgeName)
}

// SetupMgmtTapDevice creates a TAP device and attaches it to the management
// OVS bridge (provisioned by scripts/setup-ovn.sh). br-mgmt uses
// fail-mode=standalone and is excluded from ovn-bridge-mappings, so it
// behaves as a plain L2 switch outside the OVN overlay.
func SetupMgmtTapDevice(instanceID, mac, bridge string) (string, error) {
	tapName := MgmtTapName(instanceID)

	// Clean up stale TAP if present
	if _, err := os.Stat("/sys/class/net/" + tapName); err == nil {
		slog.Warn("Stale mgmt tap found, cleaning up", "tap", tapName)
		if err := sudoCommand("ovs-vsctl", "--if-exists", "del-port", bridge, tapName).Run(); err != nil {
			slog.Warn("Failed to remove stale mgmt tap from bridge", "tap", tapName, "err", err)
		}
		if err := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run(); err != nil {
			slog.Warn("Failed to delete stale mgmt tap", "tap", tapName, "err", err)
		}
	}

	// Create TAP
	if out, err := sudoCommand("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return "", fmt.Errorf("create mgmt tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// Bring up
	if out, err := sudoCommand("ip", "link", "set", tapName, "up").CombinedOutput(); err != nil {
		_ = sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return "", fmt.Errorf("bring up mgmt tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// Attach to OVS mgmt bridge
	if out, err := sudoCommand("ovs-vsctl", "--may-exist", "add-port", bridge, tapName).CombinedOutput(); err != nil {
		_ = sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return "", fmt.Errorf("add mgmt tap to %s: %s: %w", bridge, strings.TrimSpace(string(out)), err)
	}

	slog.Info("Management TAP created", "tap", tapName, "bridge", bridge, "mac", mac)
	return tapName, nil
}

// CleanupMgmtTapDevice removes a management TAP device from the mgmt OVS
// bridge and deletes it. Tolerant of missing state on either side: OVS port
// removal is gated by --if-exists, kernel tap deletion is gated on
// /sys/class/net/<tap> presence so callers may invoke this for an instance
// that never reached the SetupMgmtTapDevice step.
func CleanupMgmtTapDevice(tapName string) error {
	// Detach from whichever OVS bridge holds this tap (usually br-mgmt)
	if out, err := sudoCommand("ovs-vsctl", "--if-exists", "del-port", tapName).CombinedOutput(); err != nil {
		slog.Warn("Failed to remove mgmt tap from bridge", "tap", tapName, "err", err, "out", strings.TrimSpace(string(out)))
	}

	// Skip kernel-side delete if the tap is already gone (or was never
	// created) — avoids a misleading "Device does not exist" error log
	// when finalizeTermination cleans up an instance whose mgmt tap
	// setup never completed.
	if _, err := os.Stat("/sys/class/net/" + tapName); os.IsNotExist(err) {
		slog.Info("Management TAP already absent", "tap", tapName)
		return nil
	}

	// Delete TAP
	if out, err := sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("delete mgmt tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	slog.Info("Management TAP cleaned up", "tap", tapName)
	return nil
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
	if err := sudoCommand("ovs-vsctl", "br-exists", "br-int").Run(); err == nil {
		status.BrIntExists = true
	}

	// Check ovn-controller is running via ovs-appctl (more reliable than pgrep)
	if out, err := sudoCommand("ovs-appctl", "-t", "ovn-controller", "version").CombinedOutput(); err == nil && len(out) > 0 {
		status.OVNControllerUp = true
	}

	// Read chassis identity from OVS external_ids
	if out, err := sudoCommand("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:system-id").CombinedOutput(); err == nil {
		status.ChassisID = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}
	if out, err := sudoCommand("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:ovn-encap-ip").CombinedOutput(); err == nil {
		status.EncapIP = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}
	if out, err := sudoCommand("ovs-vsctl", "get", "Open_vSwitch", ".", "external_ids:ovn-remote").CombinedOutput(); err == nil {
		status.OVNRemote = strings.Trim(strings.TrimSpace(string(out)), "\"")
	}

	return status
}

// SetupComputeNode configures OVS for OVN on this compute node.
// It creates br-int with secure fail-mode and sets the OVN external_ids.
func SetupComputeNode(chassisID, ovnRemote, encapIP string) error {
	// Create br-int if it doesn't exist
	if out, err := sudoCommand("ovs-vsctl", "--may-exist", "add-br", "br-int").CombinedOutput(); err != nil {
		return fmt.Errorf("create br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Set fail-mode=secure (preserves flows during ovn-controller restart)
	if out, err := sudoCommand("ovs-vsctl", "set", "Bridge", "br-int", "fail-mode=secure").CombinedOutput(); err != nil {
		return fmt.Errorf("set br-int fail-mode: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Disable in-band management (prevents OVS from adding its own flows)
	if out, err := sudoCommand("ovs-vsctl", "set", "Bridge", "br-int", "other-config:disable-in-band=true").CombinedOutput(); err != nil {
		return fmt.Errorf("set br-int disable-in-band: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Bring br-int up
	if out, err := sudoCommand("ip", "link", "set", "br-int", "up").CombinedOutput(); err != nil {
		return fmt.Errorf("bring up br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Set OVN external_ids on the Open_vSwitch table
	if out, err := sudoCommand("ovs-vsctl", "set", "Open_vSwitch", ".",
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

	// Ensure the data NIC is preferred for Geneve tunnel routing.
	if err := EnsureDataRoute(encapIP); err != nil {
		slog.Warn("Failed to configure data NIC routing (Geneve tunnels may use wrong source IP)", "err", err)
	}

	return nil
}

// EnsureDataRoute ensures the kernel routes Geneve tunnel traffic through the
// data NIC (the interface holding the encap IP). When management and data NICs
// share the same subnet with equal route metrics, the kernel may pick the
// management NIC, causing Geneve packets to have the wrong source IP. Remote
// OVS nodes then drop these packets because the source doesn't match the
// expected tunnel remote_ip.
//
// Fix: find the data NIC's subnet route and replace it with a lower metric (50)
// so it's preferred over the management NIC's route (typically metric 100+).
func EnsureDataRoute(encapIP string) error {
	dataIface, err := findInterfaceByIP(encapIP)
	if err != nil {
		return fmt.Errorf("find data interface for %s: %w", encapIP, err)
	}

	// Read the existing subnet route for the data interface
	out, err := sudoCommand("ip", "-o", "-4", "route", "show", "dev", dataIface, "proto", "kernel", "scope", "link").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read routes for %s: %s: %w", dataIface, strings.TrimSpace(string(out)), err)
	}

	// Parse the subnet CIDR from the route output (e.g. "10.1.0.0/16 dev eth1 ...")
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return fmt.Errorf("no kernel route found for %s", dataIface)
	}
	subnet := fields[0]

	// Replace the route with a lower metric so the data NIC is preferred
	if out, err := sudoCommand("ip", "route", "replace", subnet,
		"dev", dataIface, "src", encapIP, "metric", "50",
	).CombinedOutput(); err != nil {
		return fmt.Errorf("replace route for %s: %s: %w", subnet, strings.TrimSpace(string(out)), err)
	}

	slog.Info("Data NIC route configured for Geneve tunnels",
		"interface", dataIface,
		"subnet", subnet,
		"encap_ip", encapIP,
		"metric", 50,
	)
	return nil
}

// findInterfaceByIP returns the network interface name that holds the given IP address.
func findInterfaceByIP(ipAddr string) (string, error) {
	targetIP := net.ParseIP(ipAddr)
	if targetIP == nil {
		return "", fmt.Errorf("invalid IP address: %s", ipAddr)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.Equal(targetIP) {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no interface found with IP %s", ipAddr)
}
