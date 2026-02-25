package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/vm"
)

// pciAddrRegexp extracts the PCI address component from a QDev path.
// Example QDev: "/machine/peripheral-anon/device[0]/virtio-backend"
// The device[N] index determines PCI enumeration order.
var pciAddrRegexp = regexp.MustCompile(`device\[(\d+)\]`)

// queryGuestDeviceMap uses QMP query-block to build a map from QEMU device ID
// (e.g. "os", "cloudinit", "vdisk-vol-xxx") to the guest device path
// (e.g. "/dev/vda", "/dev/vdb", "/dev/vdc").
//
// The mapping is derived from PCI address order: virtio-blk-pci devices are
// enumerated by the guest kernel in PCI bus order, which corresponds to the
// device index in the QDev path.
func queryGuestDeviceMap(d *Daemon, qmpClient *qmp.QMPClient, instanceID string) (map[string]string, error) {
	resp, err := d.SendQMPCommand(qmpClient, qmp.QMPCommand{Execute: "query-block"}, instanceID)
	if err != nil {
		return nil, fmt.Errorf("query-block failed: %w", err)
	}

	var devices []qmp.BlockDevice
	if err := json.Unmarshal(resp.Return, &devices); err != nil {
		return nil, fmt.Errorf("failed to parse query-block response: %w", err)
	}

	return buildDeviceMap(devices), nil
}

// buildDeviceMap takes a list of BlockDevices from QMP and returns a map
// from QEMU device ID to guest /dev/vdX path, sorted by PCI address.
func buildDeviceMap(devices []qmp.BlockDevice) map[string]string {
	type deviceEntry struct {
		name     string
		pciIndex int
	}

	var virtioDevices []deviceEntry

	for _, dev := range devices {
		if dev.QDev == "" || !strings.Contains(dev.QDev, "virtio-backend") {
			continue
		}

		pciIndex := extractPCIIndex(dev.QDev)
		if pciIndex < 0 {
			slog.Warn("Could not extract PCI index from QDev path", "device", dev.Device, "qdev", dev.QDev)
			continue
		}

		virtioDevices = append(virtioDevices, deviceEntry{
			name:     dev.Device,
			pciIndex: pciIndex,
		})
	}

	// Sort by PCI index — this determines /dev/vd* letter assignment
	sort.Slice(virtioDevices, func(i, j int) bool {
		return virtioDevices[i].pciIndex < virtioDevices[j].pciIndex
	})

	result := make(map[string]string, len(virtioDevices))
	for i, entry := range virtioDevices {
		if i >= 26 {
			slog.Warn("buildDeviceMap: more than 26 virtio devices, cannot map remaining",
				"totalDevices", len(virtioDevices))
			break
		}
		guestDev := fmt.Sprintf("/dev/vd%c", 'a'+i)
		result[entry.name] = guestDev
	}

	return result
}

// updateGuestDeviceNames queries the running VM's QMP to discover actual guest device
// paths and updates the instance's BlockDeviceMappings accordingly.
func (d *Daemon) updateGuestDeviceNames(instance *vm.VM) {
	if instance.QMPClient == nil {
		slog.Warn("updateGuestDeviceNames: QMPClient is nil, cannot discover guest device names",
			"instanceId", instance.ID)
		return
	}

	deviceMap, err := queryGuestDeviceMap(d, instance.QMPClient, instance.ID)
	if err != nil {
		slog.Warn("Failed to query guest device map, BlockDeviceMappings will use API names",
			"instanceId", instance.ID, "err", err)
		return
	}

	// Build volume ID → guest device path mapping from EBSRequests.
	// Collect under EBSRequests lock, then release before acquiring Instances lock
	// to maintain consistent lock ordering.
	instance.EBSRequests.Mu.Lock()
	volToGuest := make(map[string]string, len(instance.EBSRequests.Requests))
	for _, req := range instance.EBSRequests.Requests {
		var qemuID string
		if req.Boot {
			qemuID = "os"
		} else if req.CloudInit {
			qemuID = "cloudinit"
		} else {
			qemuID = fmt.Sprintf("vdisk-%s", req.Name)
		}
		if gd, ok := deviceMap[qemuID]; ok {
			volToGuest[req.Name] = gd
		}
	}
	instance.EBSRequests.Mu.Unlock()

	// Update BlockDeviceMappings using the mapping
	d.Instances.Mu.Lock()
	if instance.Instance != nil {
		for _, bdm := range instance.Instance.BlockDeviceMappings {
			if bdm.Ebs == nil || bdm.Ebs.VolumeId == nil || bdm.DeviceName == nil {
				continue
			}
			if gd, ok := volToGuest[*bdm.Ebs.VolumeId]; ok {
				bdm.DeviceName = &gd
			}
		}
	}
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after guest device name update", "instanceId", instance.ID, "err", err)
	}

	slog.Info("Updated guest device names", "instanceId", instance.ID, "deviceMap", deviceMap)
}

// extractPCIIndex parses the device index from a QDev path.
// For example: "/machine/peripheral-anon/device[3]/virtio-backend" → 3
func extractPCIIndex(qdev string) int {
	matches := pciAddrRegexp.FindStringSubmatch(qdev)
	if len(matches) < 2 {
		return -1
	}
	idx, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1
	}
	return idx
}
