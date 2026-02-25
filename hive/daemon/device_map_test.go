package daemon

import (
	"testing"

	"github.com/mulgadc/hive/hive/qmp"
	"github.com/stretchr/testify/assert"
)

func TestExtractPCIIndex(t *testing.T) {
	tests := []struct {
		name string
		qdev string
		want int
	}{
		{
			name: "standard peripheral-anon device",
			qdev: "/machine/peripheral-anon/device[0]/virtio-backend",
			want: 0,
		},
		{
			name: "device index 3",
			qdev: "/machine/peripheral-anon/device[3]/virtio-backend",
			want: 3,
		},
		{
			name: "hotplug device with higher index",
			qdev: "/machine/peripheral/hotplug1/device[12]/virtio-backend",
			want: 12,
		},
		{
			name: "unattached device without brackets",
			qdev: "/machine/unattached/device[24]",
			want: 24,
		},
		{
			name: "empty string",
			qdev: "",
			want: -1,
		},
		{
			name: "no device brackets",
			qdev: "/machine/peripheral/something",
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPCIIndex(tt.qdev)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBuildDeviceMap tests the core device mapping logic using the example
// QMP query-block response from qmp.go:72
func TestBuildDeviceMap(t *testing.T) {
	// Simulate the exact QMP response from the example in qmp.go
	devices := []qmp.BlockDevice{
		{
			IOStatus: "ok",
			Device:   "os",
			Locked:   false,
			Inserted: &qmp.BlockInserted{
				Image: qmp.BlockImage{
					VirtualSize: 4294967296,
					Filename:    "nbd://127.0.0.1:44801",
					Format:      "raw",
				},
			},
			QDev: "/machine/peripheral-anon/device[0]/virtio-backend",
			Type: "unknown",
		},
		{
			IOStatus: "ok",
			Device:   "cloudinit",
			Locked:   false,
			Inserted: &qmp.BlockInserted{
				Image: qmp.BlockImage{
					VirtualSize: 1048576,
					Filename:    "nbd://127.0.0.1:42911",
					Format:      "raw",
				},
				RO: true,
			},
			QDev: "/machine/peripheral-anon/device[3]/virtio-backend",
			Type: "unknown",
		},
		{
			IOStatus:  "ok",
			Device:    "ide1-cd0",
			Locked:    false,
			Removable: true,
			QDev:      "/machine/unattached/device[24]",
			Type:      "unknown",
		},
		{
			Device:    "floppy0",
			Locked:    false,
			Removable: true,
			QDev:      "/machine/unattached/device[18]",
			Type:      "unknown",
		},
		{
			Device:    "sd0",
			Locked:    false,
			Removable: true,
			Type:      "unknown",
		},
	}

	result := buildDeviceMap(devices)

	assert.Equal(t, "/dev/vda", result["os"], "root disk should be /dev/vda")
	assert.Equal(t, "/dev/vdb", result["cloudinit"], "cloudinit should be /dev/vdb")
	assert.NotContains(t, result, "ide1-cd0", "ide device should be excluded")
	assert.NotContains(t, result, "floppy0", "floppy should be excluded")
	assert.NotContains(t, result, "sd0", "sd device should be excluded")
	assert.Len(t, result, 2, "should only have 2 virtio devices")
}

func TestBuildDeviceMapWithHotplug(t *testing.T) {
	devices := []qmp.BlockDevice{
		{
			Device:   "os",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral-anon/device[0]/virtio-backend",
		},
		{
			Device:   "cloudinit",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral-anon/device[3]/virtio-backend",
		},
		{
			Device:   "vdisk-vol-abc123",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral/hotplug1/device[10]/virtio-backend",
		},
		{
			Device:   "vdisk-vol-def456",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral/hotplug2/device[11]/virtio-backend",
		},
		// Legacy devices to filter out
		{Device: "floppy0", QDev: "/machine/unattached/device[18]"},
		{Device: "sd0"},
		{Device: "ide1-cd0", QDev: "/machine/unattached/device[24]"},
	}

	result := buildDeviceMap(devices)

	assert.Equal(t, "/dev/vda", result["os"])
	assert.Equal(t, "/dev/vdb", result["cloudinit"])
	assert.Equal(t, "/dev/vdc", result["vdisk-vol-abc123"])
	assert.Equal(t, "/dev/vdd", result["vdisk-vol-def456"])
	assert.Len(t, result, 4)
}

func TestBuildDeviceMapEmpty(t *testing.T) {
	result := buildDeviceMap(nil)
	assert.Empty(t, result)
}

func TestBuildDeviceMapPCIOrdering(t *testing.T) {
	// Test that PCI ordering is correct when devices are returned out of order
	devices := []qmp.BlockDevice{
		{
			Device:   "cloudinit",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral-anon/device[5]/virtio-backend",
		},
		{
			Device:   "os",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral-anon/device[1]/virtio-backend",
		},
		{
			Device:   "vdisk-vol-123",
			Inserted: &qmp.BlockInserted{},
			QDev:     "/machine/peripheral/hotplug1/device[3]/virtio-backend",
		},
	}

	result := buildDeviceMap(devices)

	assert.Equal(t, "/dev/vda", result["os"], "lowest PCI index gets /dev/vda")
	assert.Equal(t, "/dev/vdb", result["vdisk-vol-123"], "middle PCI index gets /dev/vdb")
	assert.Equal(t, "/dev/vdc", result["cloudinit"], "highest PCI index gets /dev/vdc")
}
