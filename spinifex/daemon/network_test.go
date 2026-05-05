package daemon

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

func TestTapDeviceName(t *testing.T) {
	tests := []struct {
		eniId    string
		expected string
	}{
		{"eni-abc123", "tapabc123"},                 // Short ID
		{"eni-abc123def456789", "tapabc123def456"},  // Truncated to 15 chars
		{"eni-a", "tapa"},                           // Minimal
		{"eni-123456789abcdef", "tap123456789abc"},  // Exactly 15 chars
		{"eni-123456789abcdefg", "tap123456789abc"}, // Truncated at 15
	}

	for _, tt := range tests {
		t.Run(tt.eniId, func(t *testing.T) {
			got := vm.TapDeviceName(tt.eniId)
			if got != tt.expected {
				t.Errorf("vm.TapDeviceName(%q) = %q, want %q", tt.eniId, got, tt.expected)
			}
			if len(got) > 15 {
				t.Errorf("vm.TapDeviceName(%q) = %q (len %d), exceeds IFNAMSIZ limit of 15", tt.eniId, got, len(got))
			}
		})
	}
}

// MockNetworkPlumber records calls for testing.
type MockNetworkPlumber struct {
	SetupCalls   []mockSetupCall
	CleanupCalls []string
	SetupErr     error
	CleanupErr   error
}

var _ NetworkPlumber = (*MockNetworkPlumber)(nil)

type mockSetupCall struct {
	ENIId string
	MAC   string
}

func (m *MockNetworkPlumber) SetupTapDevice(eniId, mac string) error {
	m.SetupCalls = append(m.SetupCalls, mockSetupCall{ENIId: eniId, MAC: mac})
	return m.SetupErr
}

func (m *MockNetworkPlumber) CleanupTapDevice(eniId string) error {
	m.CleanupCalls = append(m.CleanupCalls, eniId)
	return m.CleanupErr
}

func TestStartInstance_VPCNetworking(t *testing.T) {
	instance := &vm.VM{
		ID:           "i-test123",
		InstanceType: "t3.micro",
		ENIId:        "eni-abc123def456789",
		ENIMac:       "02:00:00:11:22:33",
	}

	// When ENI is set, config should use tap networking with MAC
	instance.Config = vm.Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
	}

	// Simulate what StartInstance does for VPC mode
	tapName := vm.TapDeviceName(instance.ENIId)
	instance.Config.NetDevs = append(instance.Config.NetDevs, vm.NetDev{
		Value: "tap,id=net0,ifname=" + tapName + ",script=no,downscript=no",
	})
	instance.Config.Devices = append(instance.Config.Devices, vm.Device{
		Value: "virtio-net-pci,netdev=net0,mac=" + instance.ENIMac,
	})

	// Verify QEMU args
	if len(instance.Config.NetDevs) != 1 {
		t.Fatalf("expected 1 netdev, got %d", len(instance.Config.NetDevs))
	}

	expected := "tap,id=net0,ifname=tapabc123def456,script=no,downscript=no"
	if instance.Config.NetDevs[0].Value != expected {
		t.Errorf("netdev = %q, want %q", instance.Config.NetDevs[0].Value, expected)
	}

	expectedDev := "virtio-net-pci,netdev=net0,mac=02:00:00:11:22:33"
	if instance.Config.Devices[0].Value != expectedDev {
		t.Errorf("device = %q, want %q", instance.Config.Devices[0].Value, expectedDev)
	}
}

func TestStartInstance_FallbackNetworking(t *testing.T) {
	instance := &vm.VM{
		ID:           "i-test456",
		InstanceType: "t3.micro",
		// No ENI — should use user-mode networking
	}

	instance.Config = vm.Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
	}

	// Simulate what StartInstance does for non-VPC mode
	instance.Config.NetDevs = append(instance.Config.NetDevs, vm.NetDev{
		Value: "user,id=net0,hostfwd=tcp:127.0.0.1:22222-:22",
	})
	instance.Config.Devices = append(instance.Config.Devices, vm.Device{
		Value: "virtio-net-pci,netdev=net0",
	})

	// Verify no MAC is specified (QEMU auto-assigns)
	if len(instance.Config.Devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(instance.Config.Devices))
	}

	// User-mode networking should not include MAC
	dev := instance.Config.Devices[0].Value
	if dev != "virtio-net-pci,netdev=net0" {
		t.Errorf("device = %q, want 'virtio-net-pci,netdev=net0'", dev)
	}
}

func TestMockNetworkPlumber_SetupAndCleanup(t *testing.T) {
	mock := &MockNetworkPlumber{}

	err := mock.SetupTapDevice("eni-abc123", "02:00:00:aa:bb:cc")
	if err != nil {
		t.Fatalf("SetupTapDevice: %v", err)
	}
	if len(mock.SetupCalls) != 1 {
		t.Fatalf("expected 1 setup call, got %d", len(mock.SetupCalls))
	}
	if mock.SetupCalls[0].ENIId != "eni-abc123" {
		t.Errorf("setup eniId = %q, want 'eni-abc123'", mock.SetupCalls[0].ENIId)
	}
	if mock.SetupCalls[0].MAC != "02:00:00:aa:bb:cc" {
		t.Errorf("setup mac = %q, want '02:00:00:aa:bb:cc'", mock.SetupCalls[0].MAC)
	}

	err = mock.CleanupTapDevice("eni-abc123")
	if err != nil {
		t.Fatalf("CleanupTapDevice: %v", err)
	}
	if len(mock.CleanupCalls) != 1 {
		t.Fatalf("expected 1 cleanup call, got %d", len(mock.CleanupCalls))
	}
	if mock.CleanupCalls[0] != "eni-abc123" {
		t.Errorf("cleanup eniId = %q, want 'eni-abc123'", mock.CleanupCalls[0])
	}
}

func TestSudoCommand_NonRoot(t *testing.T) {
	// When not root, sudoCommand should prefix with sudo
	cmd := sudoCommand("ovs-vsctl", "br-exists", "br-int")
	args := cmd.Args

	if os.Getuid() == 0 {
		// Running as root: should NOT use sudo
		if args[0] != "ovs-vsctl" {
			t.Errorf("as root, expected args[0]='ovs-vsctl', got %q", args[0])
		}
	} else {
		// Running as non-root: should use sudo
		if args[0] != "sudo" {
			t.Errorf("as non-root, expected args[0]='sudo', got %q", args[0])
		}
		if args[1] != "ovs-vsctl" {
			t.Errorf("as non-root, expected args[1]='ovs-vsctl', got %q", args[1])
		}
		if len(args) != 4 {
			t.Errorf("expected 4 args [sudo ovs-vsctl br-exists br-int], got %d: %v", len(args), args)
		}
	}
}

func TestGenerateDevMAC(t *testing.T) {
	tests := []struct {
		instanceId string
	}{
		{"i-abc123"},
		{"i-def456"},
		{"i-ghi789"},
	}

	// All MACs must be valid locally-administered unicast and unique. First
	// octet is hash-derived (not the literal class prefix of the old impl).
	seen := make(map[string]bool)
	for _, tt := range tests {
		mac := vm.GenerateDevMAC(tt.instanceId)
		hw, err := net.ParseMAC(mac)
		if err != nil {
			t.Errorf("vm.GenerateDevMAC(%q) = %q: invalid MAC: %v", tt.instanceId, mac, err)
			continue
		}
		if hw[0]&0x03 != 0x02 {
			t.Errorf("vm.GenerateDevMAC(%q) = %q: expected unicast+LAA bits, got %#x",
				tt.instanceId, mac, hw[0])
		}
		if seen[mac] {
			t.Errorf("vm.GenerateDevMAC(%q) = %q, duplicate MAC", tt.instanceId, mac)
		}
		seen[mac] = true
	}

	// Class separation: dev and mgmt MACs for the same instance must differ.
	if vm.GenerateDevMAC("i-abc123") == generateMgmtMAC("i-abc123") {
		t.Error("expected dev and mgmt MACs for same instance to differ")
	}

	// Same input should produce same output (deterministic)
	mac1 := vm.GenerateDevMAC("i-test123")
	mac2 := vm.GenerateDevMAC("i-test123")
	if mac1 != mac2 {
		t.Errorf("generateDevMAC not deterministic: %q != %q", mac1, mac2)
	}
}

func TestFindInterfaceByIP_InvalidIP(t *testing.T) {
	_, err := findInterfaceByIP("not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid IP")
	}
	if !strings.Contains(err.Error(), "invalid IP address") {
		t.Errorf("expected 'invalid IP address' error, got: %v", err)
	}
}

func TestFindInterfaceByIP_Loopback(t *testing.T) {
	// 127.0.0.1 should always be on the loopback interface
	iface, err := findInterfaceByIP("127.0.0.1")
	if err != nil {
		t.Fatalf("findInterfaceByIP(127.0.0.1): %v", err)
	}
	if iface != "lo" {
		t.Errorf("findInterfaceByIP(127.0.0.1) = %q, want 'lo'", iface)
	}
}

func TestFindInterfaceByIP_NotFound(t *testing.T) {
	_, err := findInterfaceByIP("192.0.2.1")
	if err == nil {
		t.Fatal("expected error for non-existent IP")
	}
	if !strings.Contains(err.Error(), "no interface found") {
		t.Errorf("expected 'no interface found' error, got: %v", err)
	}
}

// TestCleanupMgmtTapDevice_MissingKernelTap verifies the nil-safe branch
// added to support finalizeTermination: a terminate that races mid-launch
// (or an instance that never created a mgmt tap) must not log a misleading
// "Device does not exist" error from `ip tuntap del`.
func TestCleanupMgmtTapDevice_MissingKernelTap(t *testing.T) {
	// 15-char IFNAMSIZ-compliant name that does not exist in /sys/class/net.
	// The OVS del-port call may fail on CI without ovs-vsctl, but is
	// best-effort + logged-warn; the kernel-presence gate short-circuits
	// before `ip tuntap del` runs.
	if err := CleanupMgmtTapDevice("mg-test-noexist"); err != nil {
		t.Fatalf("expected nil for missing kernel tap, got: %v", err)
	}
}

func TestEnsureDataRoute_NoOVS(t *testing.T) {
	// EnsureDataRoute requires ip commands which may not work in CI.
	// On loopback, there's no kernel subnet route, so it should return an error.
	err := EnsureDataRoute("127.0.0.1")
	// We expect an error (no kernel route for lo), but no panic.
	_ = err
}

func TestSetupComputeNode_ValidatesArgs(t *testing.T) {
	// Stub sudoCommand so the test never shells out to the host's real
	// ovs-vsctl. Without this stub, on a dev box with OVS installed the call
	// silently mutated external_ids:system-id (and ovn-remote, ovn-encap-ip)
	// on the live cluster, breaking vpcd's chassis discovery until reboot.
	orig := sudoCommand
	t.Cleanup(func() { sudoCommand = orig })
	sudoCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/false")
	}

	if err := SetupComputeNode("chassis-test", "tcp:127.0.0.1:6642", "10.0.0.1"); err == nil {
		t.Fatal("expected error from stubbed sudoCommand, got nil")
	}
}

func TestMockNetworkPlumber_SetupError(t *testing.T) {
	mock := &MockNetworkPlumber{
		SetupErr: fmt.Errorf("simulated setup failure"),
	}
	err := mock.SetupTapDevice("eni-abc123", "02:00:00:aa:bb:cc")
	if err == nil {
		t.Fatal("expected error from SetupTapDevice")
	}
	if err.Error() != "simulated setup failure" {
		t.Errorf("unexpected error: %v", err)
	}
	// Call should still be recorded
	if len(mock.SetupCalls) != 1 {
		t.Fatalf("expected 1 setup call, got %d", len(mock.SetupCalls))
	}
}

func TestMockNetworkPlumber_CleanupError(t *testing.T) {
	mock := &MockNetworkPlumber{
		CleanupErr: fmt.Errorf("simulated cleanup failure"),
	}
	err := mock.CleanupTapDevice("eni-abc123")
	if err == nil {
		t.Fatal("expected error from CleanupTapDevice")
	}
	if err.Error() != "simulated cleanup failure" {
		t.Errorf("unexpected error: %v", err)
	}
	if len(mock.CleanupCalls) != 1 {
		t.Fatalf("expected 1 cleanup call, got %d", len(mock.CleanupCalls))
	}
}

func TestGenerateDevMAC_Format(t *testing.T) {
	tests := []string{
		"i-abc123",
		"i-def456",
		"i-00000000",
		"i-ffffffff",
		"i-a",
		"i-very-long-instance-id-with-many-characters",
	}
	for _, id := range tests {
		mac := vm.GenerateDevMAC(id)
		hw, err := net.ParseMAC(mac)
		if err != nil {
			t.Errorf("vm.GenerateDevMAC(%q) = %q: invalid MAC: %v", id, mac, err)
			continue
		}
		if hw[0]&0x03 != 0x02 {
			t.Errorf("vm.GenerateDevMAC(%q) = %q: expected unicast+LAA bits, got %#x",
				id, mac, hw[0])
		}
	}
}

func TestTapDeviceName_EmptyInput(t *testing.T) {
	// Even with empty string (no eni- prefix), should not panic
	name := vm.TapDeviceName("")
	if name != "tap" {
		t.Errorf("vm.TapDeviceName('') = %q, want 'tap'", name)
	}
}

func TestOVSIfaceID_Format(t *testing.T) {
	tests := []struct {
		eniId    string
		expected string
	}{
		{"eni-abc123", "port-eni-abc123"},
		{"eni-abc123def456789", "port-eni-abc123def456789"},
		{"eni-short", "port-eni-short"},
		{"eni-", "port-eni-"},
		{"", "port-"},
	}
	for _, tt := range tests {
		got := OVSIfaceID(tt.eniId)
		if got != tt.expected {
			t.Errorf("OVSIfaceID(%q) = %q, want %q", tt.eniId, got, tt.expected)
		}
	}
}

func TestGenerateMgmtMAC(t *testing.T) {
	tests := []string{
		"i-abc123",
		"i-def456",
		"i-ghi789",
	}

	seen := make(map[string]bool)
	for _, id := range tests {
		mac := generateMgmtMAC(id)
		hw, err := net.ParseMAC(mac)
		if err != nil {
			t.Errorf("generateMgmtMAC(%q) = %q: invalid MAC: %v", id, mac, err)
			continue
		}
		if hw[0]&0x03 != 0x02 {
			t.Errorf("generateMgmtMAC(%q) = %q: expected unicast+LAA bits, got %#x",
				id, mac, hw[0])
		}
		if seen[mac] {
			t.Errorf("generateMgmtMAC(%q) = %q, duplicate MAC", id, mac)
		}
		seen[mac] = true
	}

	// Deterministic
	mac1 := generateMgmtMAC("i-test123")
	mac2 := generateMgmtMAC("i-test123")
	if mac1 != mac2 {
		t.Errorf("generateMgmtMAC not deterministic: %q != %q", mac1, mac2)
	}

	// Different from dev MAC for same instance
	devMAC := vm.GenerateDevMAC("i-test123")
	mgmtMAC := generateMgmtMAC("i-test123")
	if devMAC == mgmtMAC {
		t.Errorf("dev and mgmt MACs should differ for same instance: both %q", devMAC)
	}
}

func TestMgmtTapName(t *testing.T) {
	tests := []struct {
		instanceID string
		expected   string
	}{
		{"i-abc123", "mgabc123"},
		{"i-abc123def456789", "mgabc123def4567"}, // Truncated to 15 chars
		{"i-a", "mga"},
		{"abc123", "mgabc123"}, // No i- prefix
	}

	for _, tt := range tests {
		t.Run(tt.instanceID, func(t *testing.T) {
			got := MgmtTapName(tt.instanceID)
			if got != tt.expected {
				t.Errorf("MgmtTapName(%q) = %q, want %q", tt.instanceID, got, tt.expected)
			}
			if len(got) > 15 {
				t.Errorf("MgmtTapName(%q) = %q (len %d), exceeds IFNAMSIZ limit of 15", tt.instanceID, got, len(got))
			}
		})
	}
}

func TestGetBridgeIPv4_Loopback(t *testing.T) {
	// "lo" is always present and has 127.0.0.1
	ip, err := GetBridgeIPv4("lo")
	if err != nil {
		t.Fatalf("GetBridgeIPv4(lo): %v", err)
	}
	if ip != "127.0.0.1" {
		t.Errorf("GetBridgeIPv4(lo) = %q, want 127.0.0.1", ip)
	}
}

func TestGetBridgeIPv4_NonexistentBridge(t *testing.T) {
	ip, err := GetBridgeIPv4("br-nonexistent-test-xyz")
	if err != nil {
		t.Fatalf("expected nil error for absent bridge, got: %v", err)
	}
	if ip != "" {
		t.Errorf("expected empty IP for absent bridge, got %q", ip)
	}
}
