package vm

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/qmp"
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
			name: "unattached device path",
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

// TestBuildDeviceMap covers the core device-mapping logic across boot devices,
// hot-plugged virtio peripherals, non-virtio devices that must be filtered out,
// PCI-index ordering across mixed boot/hot-plug input, and the empty case.
func TestBuildDeviceMap(t *testing.T) {
	tests := []struct {
		name    string
		devices []qmp.BlockDevice
		want    map[string]string // expected entries (subset checked with Equal)
		absent  []string          // keys that must NOT appear
		wantLen int
	}{
		{
			name:    "empty input",
			devices: nil,
			want:    map[string]string{},
			wantLen: 0,
		},
		{
			// Mirrors the example QMP query-block response from qmp.go:72
			name: "boot devices with non-virtio devices filtered out",
			devices: []qmp.BlockDevice{
				{
					IOStatus: "ok",
					Device:   "os",
					Inserted: &qmp.BlockInserted{
						Image: qmp.BlockImage{VirtualSize: 4294967296, Filename: "nbd://127.0.0.1:44801", Format: "raw"},
					},
					QDev: "/machine/peripheral-anon/device[0]/virtio-backend",
					Type: "unknown",
				},
				{
					IOStatus: "ok",
					Device:   "cloudinit",
					Inserted: &qmp.BlockInserted{
						Image: qmp.BlockImage{VirtualSize: 1048576, Filename: "nbd://127.0.0.1:42911", Format: "raw"},
						RO:    true,
					},
					QDev: "/machine/peripheral-anon/device[3]/virtio-backend",
					Type: "unknown",
				},
				{Device: "ide1-cd0", Removable: true, QDev: "/machine/unattached/device[24]"},
				{Device: "floppy0", Removable: true, QDev: "/machine/unattached/device[18]"},
				{Device: "sd0", Removable: true},
			},
			want:    map[string]string{"os": "/dev/vda", "cloudinit": "/dev/vdb"},
			absent:  []string{"ide1-cd0", "floppy0", "sd0"},
			wantLen: 2,
		},
		{
			// Hot-plugged devices have empty Device field and use the
			// /machine/peripheral/<id>/virtio-backend QDev path format.
			name: "boot plus hot-plugged devices",
			devices: []qmp.BlockDevice{
				{Device: "os", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral-anon/device[0]/virtio-backend"},
				{Device: "cloudinit", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral-anon/device[3]/virtio-backend"},
				{Device: "", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral/vdisk-vol-abc123/virtio-backend"},
				{Device: "", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral/vdisk-vol-def456/virtio-backend"},
				{Device: "floppy0", QDev: "/machine/unattached/device[18]"},
				{Device: "sd0"},
				{Device: "ide1-cd0", QDev: "/machine/unattached/device[24]"},
			},
			want: map[string]string{
				"os":               "/dev/vda",
				"cloudinit":        "/dev/vdb",
				"vdisk-vol-abc123": "/dev/vdc",
				"vdisk-vol-def456": "/dev/vdd",
			},
			wantLen: 4,
		},
		{
			// Boot devices returned out of order: lowest PCI index wins /dev/vda;
			// hot-plugged devices sort after boot devices.
			name: "PCI-index ordering across mixed boot and hot-plug",
			devices: []qmp.BlockDevice{
				{Device: "cloudinit", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral-anon/device[5]/virtio-backend"},
				{Device: "os", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral-anon/device[1]/virtio-backend"},
				{Device: "", Inserted: &qmp.BlockInserted{}, QDev: "/machine/peripheral/vdisk-vol-123/virtio-backend"},
			},
			want: map[string]string{
				"os":            "/dev/vda",
				"cloudinit":     "/dev/vdb",
				"vdisk-vol-123": "/dev/vdc",
			},
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDeviceMap(tt.devices)
			for k, v := range tt.want {
				assert.Equal(t, v, got[k], "device %q", k)
			}
			for _, k := range tt.absent {
				assert.NotContains(t, got, k)
			}
			assert.Len(t, got, tt.wantLen)
		})
	}
}

func TestExtractPeripheralName(t *testing.T) {
	tests := []struct {
		name string
		qdev string
		want string
	}{
		{
			name: "hot-plugged volume",
			qdev: "/machine/peripheral/vdisk-vol-abc123/virtio-backend",
			want: "vdisk-vol-abc123",
		},
		{
			name: "boot device path",
			qdev: "/machine/peripheral-anon/device[0]/virtio-backend",
			want: "",
		},
		{
			name: "empty",
			qdev: "",
			want: "",
		},
		{
			name: "no trailing slash",
			qdev: "/machine/peripheral/",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractPeripheralName(tt.qdev))
		})
	}
}

func TestExtractHotplugPort(t *testing.T) {
	tests := []struct {
		name string
		qdev string
		want int
	}{
		{
			name: "hotplug port 3",
			qdev: "/machine/peripheral/vdisk-vol-xxx/hotplug3/virtio-backend",
			want: 3,
		},
		{
			name: "no hotplug in path",
			qdev: "/machine/peripheral/vdisk-vol-xxx/virtio-backend",
			want: -1,
		},
		{
			name: "boot device",
			qdev: "/machine/peripheral-anon/device[0]/virtio-backend",
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractHotplugPort(tt.qdev))
		})
	}
}
