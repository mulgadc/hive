package vm

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/mulgadc/spinifex/spinifex/utils"
)

// TapDeviceName returns the Linux tap device name for an ENI.
// Linux IFNAMSIZ limits interface names to 15 characters; long ENI IDs are
// truncated to fit.
func TapDeviceName(eniID string) string {
	id := strings.TrimPrefix(eniID, "eni-")
	name := "tap" + id
	if len(name) > 15 {
		name = name[:15]
	}
	return name
}

// GenerateDevMAC returns the locally-administered unicast MAC for the
// dev/hostfwd NIC. The "dev:" tag disambiguates from the mgmt NIC of the
// same instance (which shares instanceID).
func GenerateDevMAC(instanceID string) string {
	return utils.HashMAC("dev:" + instanceID)
}

// setupExtraENINICs creates tap devices on br-int and appends matching QEMU
// virtio-net device entries to instance.Config for each additional ENI a
// system VM spans. The primary ENI is handled separately by the launch
// caller. Cloud-init brings the guest interfaces up via per-MAC DHCP blocks
// written by generateNetworkConfig.
func (m *Manager) setupExtraENINICs(instance *VM) error {
	if m.deps.NetworkPlumber == nil {
		return nil
	}
	for idx, extra := range instance.ExtraENIs {
		if err := m.deps.NetworkPlumber.SetupTapDevice(extra.ENIID, extra.ENIMac); err != nil {
			slog.Error("Failed to set up tap device for extra ENI", "eni", extra.ENIID, "err", err)
			return fmt.Errorf("setup tap device for extra ENI %s: %w", extra.ENIID, err)
		}
		extraTapName := TapDeviceName(extra.ENIID)
		netID := fmt.Sprintf("net%d", idx+1)
		instance.Config.NetDevs = append(instance.Config.NetDevs, NetDev{
			Value: fmt.Sprintf("tap,id=%s,ifname=%s,script=no,downscript=no", netID, extraTapName),
		})
		instance.Config.Devices = append(instance.Config.Devices, Device{
			Value: fmt.Sprintf("virtio-net-pci,netdev=%s,mac=%s", netID, extra.ENIMac),
		})
		slog.Info("Extra VPC NIC configured",
			"tap", extraTapName, "eni", extra.ENIID, "mac", extra.ENIMac, "subnet", extra.SubnetID)
	}
	return nil
}
