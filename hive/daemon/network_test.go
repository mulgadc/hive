package daemon

import (
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

func TestNetworkPlumber_InterfaceCompliance(t *testing.T) {
	// Verify both types satisfy the interface
	var _ NetworkPlumber = &OVSNetworkPlumber{}
	var _ NetworkPlumber = &MockNetworkPlumber{}
}
