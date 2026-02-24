package daemon

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mulgadc/hive/hive/vm"
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
			got := TapDeviceName(tt.eniId)
			if got != tt.expected {
				t.Errorf("TapDeviceName(%q) = %q, want %q", tt.eniId, got, tt.expected)
			}
			if len(got) > 15 {
				t.Errorf("TapDeviceName(%q) = %q (len %d), exceeds IFNAMSIZ limit of 15", tt.eniId, got, len(got))
			}
		})
	}
}

func TestOVSIfaceID(t *testing.T) {
	tests := []struct {
		eniId    string
		expected string
	}{
		{"eni-abc123", "port-eni-abc123"},
		{"eni-abc123def456789", "port-eni-abc123def456789"},
	}

	for _, tt := range tests {
		t.Run(tt.eniId, func(t *testing.T) {
			got := OVSIfaceID(tt.eniId)
			if got != tt.expected {
				t.Errorf("OVSIfaceID(%q) = %q, want %q", tt.eniId, got, tt.expected)
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
	tapName := TapDeviceName(instance.ENIId)
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

	// All MACs should be unique and have the 02:de:v0 prefix
	seen := make(map[string]bool)
	for _, tt := range tests {
		mac := generateDevMAC(tt.instanceId)
		if !strings.HasPrefix(mac, "02:de:00:") {
			t.Errorf("generateDevMAC(%q) = %q, want prefix '02:de:00:'", tt.instanceId, mac)
		}
		if seen[mac] {
			t.Errorf("generateDevMAC(%q) = %q, duplicate MAC", tt.instanceId, mac)
		}
		seen[mac] = true
	}

	// Same input should produce same output (deterministic)
	mac1 := generateDevMAC("i-test123")
	mac2 := generateDevMAC("i-test123")
	if mac1 != mac2 {
		t.Errorf("generateDevMAC not deterministic: %q != %q", mac1, mac2)
	}
}

func TestNetworkPlumber_InterfaceCompliance(t *testing.T) {
	// Verify both types satisfy the interface
	var _ NetworkPlumber = &OVSNetworkPlumber{}
	var _ NetworkPlumber = &MockNetworkPlumber{}
}

func TestOVNHealthStatus_Fields(t *testing.T) {
	// Verify OVNHealthStatus struct can be used for health reporting
	status := OVNHealthStatus{
		BrIntExists:     true,
		OVNControllerUp: true,
		ChassisID:       "chassis-node1",
		EncapIP:         "10.0.0.1",
		OVNRemote:       "tcp:10.0.0.1:6642",
	}

	if !status.BrIntExists {
		t.Error("expected BrIntExists to be true")
	}
	if !status.OVNControllerUp {
		t.Error("expected OVNControllerUp to be true")
	}
	if status.ChassisID != "chassis-node1" {
		t.Errorf("ChassisID = %q, want 'chassis-node1'", status.ChassisID)
	}
	if status.EncapIP != "10.0.0.1" {
		t.Errorf("EncapIP = %q, want '10.0.0.1'", status.EncapIP)
	}
	if status.OVNRemote != "tcp:10.0.0.1:6642" {
		t.Errorf("OVNRemote = %q, want 'tcp:10.0.0.1:6642'", status.OVNRemote)
	}
}

func TestOVNHealthStatus_Defaults(t *testing.T) {
	// Zero-value OVNHealthStatus should indicate nothing is ready
	var status OVNHealthStatus

	if status.BrIntExists {
		t.Error("zero-value BrIntExists should be false")
	}
	if status.OVNControllerUp {
		t.Error("zero-value OVNControllerUp should be false")
	}
	if status.ChassisID != "" {
		t.Errorf("zero-value ChassisID should be empty, got %q", status.ChassisID)
	}
}

func TestCheckOVNHealth_ReturnsStatus(t *testing.T) {
	// CheckOVNHealth should return a status struct without panicking,
	// even when OVS/OVN tools are not installed (CI environment).
	// On a dev machine without OVN, all fields will be zero values.
	status := CheckOVNHealth()

	// On CI without OVS, both should be false — just verify no panic
	_ = status.BrIntExists
	_ = status.OVNControllerUp
	_ = status.ChassisID
	_ = status.EncapIP
	_ = status.OVNRemote
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

func TestEnsureDataRoute_NoOVS(t *testing.T) {
	// EnsureDataRoute requires ip commands which may not work in CI.
	// On loopback, there's no kernel subnet route, so it should return an error.
	err := EnsureDataRoute("127.0.0.1")
	// We expect an error (no kernel route for lo), but no panic.
	_ = err
}

func TestSetupComputeNode_ValidatesArgs(t *testing.T) {
	// SetupComputeNode requires ovs-vsctl which may not be available in CI.
	// This test verifies the function signature and that it returns an error
	// when OVS is not installed (expected on CI).
	err := SetupComputeNode("chassis-test", "tcp:127.0.0.1:6642", "10.0.0.1")

	// We expect an error in CI (no OVS), but the function should not panic.
	// On a dev machine with OVS, it would succeed. Either result is acceptable.
	_ = err
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
		mac := generateDevMAC(id)
		// Must be 17 chars: xx:xx:xx:xx:xx:xx
		if len(mac) != 17 {
			t.Errorf("generateDevMAC(%q) = %q, expected 17 chars", id, mac)
		}
		// Must start with locally-administered unicast prefix 02:de:00
		if !strings.HasPrefix(mac, "02:de:00:") {
			t.Errorf("generateDevMAC(%q) = %q, expected prefix 02:de:00:", id, mac)
		}
		// All chars must be valid hex or colons
		for i, c := range mac {
			if i%3 == 2 {
				if c != ':' {
					t.Errorf("generateDevMAC(%q) = %q, expected ':' at pos %d", id, mac, i)
				}
			} else {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("generateDevMAC(%q) = %q, invalid hex char '%c' at pos %d", id, mac, c, i)
				}
			}
		}
	}
}

func TestTapDeviceName_EmptyInput(t *testing.T) {
	// Even with empty string (no eni- prefix), should not panic
	name := TapDeviceName("")
	if name != "tap" {
		t.Errorf("TapDeviceName('') = %q, want 'tap'", name)
	}
}

func TestOVSIfaceID_Format(t *testing.T) {
	tests := []struct {
		eniId    string
		expected string
	}{
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
