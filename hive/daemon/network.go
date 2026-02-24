package daemon

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
)

// sudoCommand wraps exec.Command with sudo when running as non-root.
// OVS/OVN and ip commands require elevated privileges; in Docker and
// production the daemon runs as root, but in dev environments it may not.
func sudoCommand(name string, args ...string) *exec.Cmd {
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

func (p *OVSNetworkPlumber) SetupTapDevice(eniId, mac string) error {
	tapName := TapDeviceName(eniId)
	ifaceID := OVSIfaceID(eniId)

	// 0. If tap already exists (e.g. unclean shutdown), clean it up first
	if _, err := os.Stat("/sys/class/net/" + tapName); err == nil {
		slog.Warn("Stale tap device found, cleaning up before recreate", "tap", tapName)
		_ = sudoCommand("ovs-vsctl", "--if-exists", "del-port", "br-int", tapName).Run()
		_ = sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
	}

	// 1. Create tap device
	if out, err := sudoCommand("ip", "tuntap", "add", "dev", tapName, "mode", "tap").CombinedOutput(); err != nil {
		return fmt.Errorf("create tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 2. Bring tap up
	if out, err := sudoCommand("ip", "link", "set", tapName, "up").CombinedOutput(); err != nil {
		_ = sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return fmt.Errorf("bring up tap %s: %s: %w", tapName, strings.TrimSpace(string(out)), err)
	}

	// 3. Add to br-int with iface-id for OVN port binding
	if out, err := sudoCommand("ovs-vsctl",
		"add-port", "br-int", tapName,
		"--", "set", "Interface", tapName,
		fmt.Sprintf("external_ids:iface-id=%s", ifaceID),
		fmt.Sprintf("external_ids:attached-mac=%s", mac),
	).CombinedOutput(); err != nil {
		_ = sudoCommand("ip", "tuntap", "del", "dev", tapName, "mode", "tap").Run()
		return fmt.Errorf("add tap to br-int: %s: %w", strings.TrimSpace(string(out)), err)
	}

	slog.Info("Network plumbing complete", "tap", tapName, "iface-id", ifaceID, "mac", mac)
	return nil
}

func (p *OVSNetworkPlumber) CleanupTapDevice(eniId string) error {
	tapName := TapDeviceName(eniId)

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

// generateDevMAC creates a locally-administered unicast MAC for the dev/hostfwd NIC.
// Uses prefix 02:de:00 to distinguish from ENI MACs (02:00:00).
// All octets must be valid hex for QEMU's virtio-net-pci mac property.
func generateDevMAC(instanceId string) string {
	h := uint32(0)
	for _, c := range instanceId {
		h = h*31 + uint32(c)
	}
	return fmt.Sprintf("02:de:00:%02x:%02x:%02x", (h>>16)&0xff, (h>>8)&0xff, h&0xff)
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
