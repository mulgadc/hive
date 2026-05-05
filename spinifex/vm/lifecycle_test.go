package vm

import (
	"fmt"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildBaseVMConfig(t *testing.T) {
	tests := []struct {
		name         string
		instanceID   string
		pidFile      string
		consolePath  string
		serialSocket string
		architecture string
		vCPUs        int
		memoryMiB    int
	}{
		{
			name:         "x86_64 instance",
			instanceID:   "i-abc123",
			pidFile:      "/tmp/qemu-i-abc123.pid",
			consolePath:  "/run/console-i-abc123.log",
			serialSocket: "/run/serial-i-abc123.sock",
			architecture: "x86_64",
			vCPUs:        4,
			memoryMiB:    8192,
		},
		{
			name:         "arm64 instance",
			instanceID:   "i-def456",
			pidFile:      "/tmp/qemu-i-def456.pid",
			consolePath:  "/run/console-i-def456.log",
			serialSocket: "/run/serial-i-def456.sock",
			architecture: "arm64",
			vCPUs:        2,
			memoryMiB:    4096,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := buildBaseVMConfig(tt.instanceID, tt.pidFile, tt.consolePath, tt.serialSocket, tt.architecture, tt.vCPUs, tt.memoryMiB)

			assert.Equal(t, tt.instanceID, cfg.Name)
			assert.Equal(t, tt.pidFile, cfg.PIDFile)
			assert.Equal(t, tt.consolePath, cfg.ConsoleLogPath)
			assert.Equal(t, tt.serialSocket, cfg.SerialSocket)
			assert.Equal(t, tt.architecture, cfg.Architecture)
			assert.Equal(t, tt.vCPUs, cfg.CPUCount)
			assert.Equal(t, tt.memoryMiB, cfg.Memory)
			assert.True(t, cfg.EnableKVM)
			assert.True(t, cfg.NoGraphic)
			assert.Equal(t, "q35", cfg.MachineType)
			assert.Equal(t, "host", cfg.CPUType)

			require.Len(t, cfg.Devices, 11)
			for i, dev := range cfg.Devices {
				expected := fmt.Sprintf("pcie-root-port,id=hotplug%d,chassis=%d,slot=0", i+1, i+1)
				assert.Equal(t, expected, dev.Value)
			}
		})
	}
}

func TestBuildDrives(t *testing.T) {
	tests := []struct {
		name          string
		requests      []types.EBSRequest
		cpuCount      int
		wantDrives    int
		wantIOThreads int
		wantDevices   int
		wantErr       string
	}{
		{
			name: "boot volume",
			requests: []types.EBSRequest{
				{Name: "vol-boot", NBDURI: "nbd:unix:/tmp/boot.sock", Boot: true},
			},
			cpuCount:      4,
			wantDrives:    1,
			wantIOThreads: 1,
			wantDevices:   1,
		},
		{
			name: "cloud-init volume",
			requests: []types.EBSRequest{
				{Name: "vol-ci", NBDURI: "nbd:unix:/tmp/ci.sock", CloudInit: true},
			},
			cpuCount:      2,
			wantDrives:    1,
			wantIOThreads: 0,
			wantDevices:   0,
		},
		{
			name: "EFI volume skipped",
			requests: []types.EBSRequest{
				{Name: "vol-efi", EFI: true},
			},
			cpuCount:      2,
			wantDrives:    0,
			wantIOThreads: 0,
			wantDevices:   0,
		},
		{
			name: "missing NBDURI returns error",
			requests: []types.EBSRequest{
				{Name: "vol-bad"},
			},
			cpuCount: 2,
			wantErr:  "NBDURI not set for volume vol-bad",
		},
		{
			name: "mixed boot + cloud-init + EFI",
			requests: []types.EBSRequest{
				{Name: "vol-boot", NBDURI: "nbd:unix:/tmp/boot.sock", Boot: true},
				{Name: "vol-ci", NBDURI: "nbd:unix:/tmp/ci.sock", CloudInit: true},
				{Name: "vol-efi", EFI: true},
			},
			cpuCount:      4,
			wantDrives:    2,
			wantIOThreads: 1,
			wantDevices:   1,
		},
		{
			name:          "empty requests",
			requests:      []types.EBSRequest{},
			cpuCount:      2,
			wantDrives:    0,
			wantIOThreads: 0,
			wantDevices:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drives, iothreads, devices, err := buildDrives(tt.requests, tt.cpuCount)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, drives, tt.wantDrives)
			assert.Len(t, iothreads, tt.wantIOThreads)
			assert.Len(t, devices, tt.wantDevices)
		})
	}
}

func TestBuildDrives_BootVolume(t *testing.T) {
	requests := []types.EBSRequest{
		{Name: "vol-boot", NBDURI: "nbd:unix:/tmp/boot.sock", Boot: true},
	}

	drives, iothreads, devices, err := buildDrives(requests, 4)
	require.NoError(t, err)

	require.Len(t, drives, 1)
	d := drives[0]
	assert.Equal(t, "nbd:unix:/tmp/boot.sock", d.File)
	assert.Equal(t, "raw", d.Format)
	assert.Equal(t, "none", d.If)
	assert.Equal(t, "disk", d.Media)
	assert.Equal(t, "os", d.ID)
	assert.Equal(t, "none", d.Cache)

	require.Len(t, iothreads, 1)
	assert.Equal(t, "ioth-os", iothreads[0].ID)

	require.Len(t, devices, 1)
	assert.Equal(t, "virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=4,bootindex=1", devices[0].Value)
}

func TestBuildDrives_CloudInitVolume(t *testing.T) {
	requests := []types.EBSRequest{
		{Name: "vol-ci", NBDURI: "nbd:unix:/tmp/ci.sock", CloudInit: true},
	}

	drives, _, _, err := buildDrives(requests, 2)
	require.NoError(t, err)

	require.Len(t, drives, 1)
	d := drives[0]
	assert.Equal(t, "nbd:unix:/tmp/ci.sock", d.File)
	assert.Equal(t, "raw", d.Format)
	assert.Equal(t, "virtio", d.If)
	assert.Equal(t, "cdrom", d.Media)
	assert.Equal(t, "cloudinit", d.ID)
}

func TestTapDeviceName(t *testing.T) {
	tests := []struct {
		name string
		eni  string
		want string
	}{
		{"short ENI", "eni-abc123", "tapabc123"},
		{"prefix-only", "abc123", "tapabc123"},
		{"truncate to 15", "eni-abc123def456789", "tapabc123def456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TapDeviceName(tt.eni))
		})
	}
}

func TestGenerateDevMAC_Stable(t *testing.T) {
	a := GenerateDevMAC("i-abc123")
	b := GenerateDevMAC("i-abc123")
	assert.Equal(t, a, b, "MAC must be deterministic for the same instance ID")
	assert.NotEqual(t, GenerateDevMAC("i-abc123"), GenerateDevMAC("i-def456"))
}

func TestStartReturnsErrorWhenInstanceUnknown(t *testing.T) {
	m := NewManager()
	err := m.Start("i-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "i-missing")
}
