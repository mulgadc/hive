/*
Copyright © 2026 Mulga Defense Corporation

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

// Package autoinstall scans boot media for an autoinstall.toml and, when
// headless mode is enabled, converts it into an install.Config that the
// installer can run without any user interaction.
package autoinstall

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mulgadc/spinifex/cmd/installer/install"
	"github.com/pelletier/go-toml/v2"
)

const configFileName = "autoinstall.toml"

// fileConfig mirrors the structure of autoinstall.toml.
type fileConfig struct {
	Autoinstall struct {
		Enabled bool `toml:"enabled"`
	} `toml:"autoinstall"`

	Node struct {
		Hostname string `toml:"hostname"`
		Password string `toml:"password"`
	} `toml:"node"`

	Disk struct {
		Target string `toml:"target"`
	} `toml:"disk"`

	Network struct {
		WAN ifaceConfig `toml:"wan"`
		LAN ifaceConfig `toml:"lan"`
	} `toml:"network"`

	Cluster struct {
		Role     string `toml:"role"`
		JoinAddr string `toml:"join_addr"`
	} `toml:"cluster"`
}

type ifaceConfig struct {
	Interface string   `toml:"interface"`
	Mode      string   `toml:"mode"`    // "dhcp" or "static"
	Address   string   `toml:"address"` // static only
	Mask      string   `toml:"mask"`    // static only
	Gateway   string   `toml:"gateway"` // static only, WAN only
	DNS       []string `toml:"dns"`     // static only
}

// Load scans boot media for an autoinstall.toml. Returns nil if the file is
// not found or enabled = false. Returns an error only if a config was found
// but could not be parsed or is invalid.
func Load() (*install.Config, error) {
	path, mountpoint, err := findConfigFile()
	if err != nil {
		slog.Debug("autoinstall scan failed", "err", err)
		return nil, nil
	}
	if path == "" {
		slog.Debug("autoinstall: no config found, using interactive mode")
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read autoinstall config: %w", err)
	}

	var fc fileConfig
	if err := toml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse autoinstall config: %w", err)
	}

	if !fc.Autoinstall.Enabled {
		slog.Info("autoinstall: config found but enabled = false, using interactive mode")
		return nil, nil
	}

	slog.Info("autoinstall: headless mode enabled", "config", path)

	cfg, err := toInstallConfig(fc)
	if err != nil {
		return nil, fmt.Errorf("autoinstall config invalid: %w", err)
	}

	// Persist the mountpoint so EjectAndReboot knows which device to eject.
	if mountpoint != "" {
		_ = os.WriteFile("/run/spinifex-autoinstall-src", []byte(mountpoint), 0o600)
	}

	return cfg, nil
}

// EjectAndReboot ejects the USB that provided the autoinstall config (if
// identifiable) then reboots. Call this after a successful headless install to
// prevent the node looping back into the installer on next boot.
func EjectAndReboot() {
	mp, _ := os.ReadFile("/run/spinifex-autoinstall-src")
	mountpoint := strings.TrimSpace(string(mp))
	if mountpoint != "" {
		if dev := deviceForMountpoint(mountpoint); dev != "" {
			// Walk up from partition (/dev/sdb1) to the whole disk (/dev/sdb).
			disk := strings.TrimRight(dev, "0123456789")
			slog.Info("autoinstall: ejecting source device", "disk", disk)
			_ = exec.Command("eject", disk).Run()
		}
		_ = exec.Command("umount", mountpoint).Run()
		_ = os.Remove(mountpoint)
	}

	fmt.Println()
	fmt.Println("Installation complete.")
	fmt.Println("Remove the USB drive now if it was not ejected automatically.")
	fmt.Println("Rebooting in 10 seconds...")
	time.Sleep(10 * time.Second)
	_ = exec.Command("reboot").Run()
}

// findConfigFile returns the path of autoinstall.toml and its mountpoint (if
// the scanner had to mount a partition to find it). Skips iso9660 mounts so
// the read-only reference copy bundled in the ISO never triggers headless mode.
func findConfigFile() (path, mountpoint string, err error) {
	// Prefer a file on a writable (non-iso9660) already-mounted filesystem.
	mounts, _ := os.ReadFile("/proc/mounts")
	for line := range strings.SplitSeq(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		fstype := fields[2]
		mp := fields[1]
		// Skip virtual and read-only filesystems.
		switch fstype {
		case "iso9660", "squashfs", "tmpfs", "proc", "sysfs", "devtmpfs", "devpts":
			continue
		}
		candidate := filepath.Join(mp, configFileName)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, "", nil
		}
	}

	// Fall back to scanning the USB that the ISO was loaded from.
	srcDev, _ := os.ReadFile("/run/iso-dev")
	baseDev := strings.TrimSpace(string(srcDev))
	if baseDev == "" {
		return "", "", nil
	}
	// Strip partition suffix to get the disk name (e.g. /dev/sdb1 → sdb).
	diskName := filepath.Base(strings.TrimRight(baseDev, "0123456789"))

	partEntries, readErr := os.ReadDir("/sys/block/" + diskName)
	if readErr != nil {
		return "", "", nil
	}
	for _, pe := range partEntries {
		partName := pe.Name()
		if !strings.HasPrefix(partName, diskName) {
			continue
		}
		partDev := "/dev/" + partName

		// Skip if already mounted as iso9660.
		if mountedAs(partDev) == "iso9660" {
			continue
		}

		mp, err := mountReadOnly(partDev)
		if err != nil {
			continue
		}

		candidate := filepath.Join(mp, configFileName)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, mp, nil
		}

		// Not here — unmount and try next partition.
		_ = exec.Command("umount", mp).Run()
		_ = os.Remove(mp)
	}

	return "", "", nil
}

// mountReadOnly mounts partDev read-only under a temporary directory, trying
// vfat (EFI) then ext4 then a generic auto-detect. Returns the mountpoint.
func mountReadOnly(partDev string) (string, error) {
	mp := "/tmp/spinifex-cfg-" + filepath.Base(partDev)
	if err := os.MkdirAll(mp, 0o700); err != nil {
		return "", err
	}
	for _, fstype := range []string{"vfat", "ext4", "auto"} {
		args := []string{"-t", fstype, "-o", "ro", partDev, mp}
		if fstype == "auto" {
			args = []string{"-o", "ro", partDev, mp}
		}
		if exec.Command("mount", args...).Run() == nil {
			return mp, nil
		}
	}
	_ = os.Remove(mp)
	return "", fmt.Errorf("could not mount %s", partDev)
}

// mountedAs returns the filesystem type that partDev is currently mounted with,
// or an empty string if it is not mounted.
func mountedAs(partDev string) string {
	data, _ := os.ReadFile("/proc/mounts")
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == partDev {
			return fields[2]
		}
	}
	return ""
}

// deviceForMountpoint returns the block device mounted at mp, or empty string.
func deviceForMountpoint(mp string) string {
	data, _ := os.ReadFile("/proc/mounts")
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == mp {
			return fields[0]
		}
	}
	return ""
}

// toInstallConfig converts a parsed fileConfig into an install.Config,
// applying defaults and validating required fields.
func toInstallConfig(fc fileConfig) (*install.Config, error) {
	if fc.Node.Password == "" {
		return nil, fmt.Errorf("node.password is required")
	}

	hostname := fc.Node.Hostname
	if hostname == "" {
		hostname = "spinifex-node"
	}

	disk, err := resolveDisk(fc.Disk.Target)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}

	wanNIC, err := resolveNIC(fc.Network.WAN.Interface, "")
	if err != nil {
		return nil, fmt.Errorf("network.wan.interface: %w", err)
	}

	cfg := &install.Config{
		Disk:         disk,
		Hostname:     hostname,
		RootPassword: fc.Node.Password,
		WANInterface: wanNIC,
		WANDHCPMode:  strings.ToLower(fc.Network.WAN.Mode) != "static",
	}

	if !cfg.WANDHCPMode {
		if fc.Network.WAN.Address == "" || fc.Network.WAN.Mask == "" || fc.Network.WAN.Gateway == "" {
			return nil, fmt.Errorf("network.wan: address, mask, and gateway are required for mode = \"static\"")
		}
		cfg.WANAddress = fc.Network.WAN.Address
		cfg.WANMask = fc.Network.WAN.Mask
		cfg.WANGateway = fc.Network.WAN.Gateway
		cfg.WANDNS = fc.Network.WAN.DNS
	}

	// LAN is optional — only configured when interface is specified.
	if fc.Network.LAN.Interface != "" {
		lanNIC, err := resolveNIC(fc.Network.LAN.Interface, wanNIC)
		if err != nil {
			return nil, fmt.Errorf("network.lan.interface: %w", err)
		}
		cfg.LANInterface = lanNIC
		cfg.LANDHCPMode = strings.ToLower(fc.Network.LAN.Mode) != "static"
		if !cfg.LANDHCPMode {
			if fc.Network.LAN.Address == "" || fc.Network.LAN.Mask == "" {
				return nil, fmt.Errorf("network.lan: address and mask are required for mode = \"static\"")
			}
			cfg.LANAddress = fc.Network.LAN.Address
			cfg.LANMask = fc.Network.LAN.Mask
			cfg.LANDNS = fc.Network.LAN.DNS
		}
	}

	role := strings.ToLower(fc.Cluster.Role)
	if role == "" {
		role = "init"
	}
	cfg.ClusterRole = role
	if role == "join" {
		if fc.Cluster.JoinAddr == "" {
			return nil, fmt.Errorf("cluster.join_addr is required when role = \"join\"")
		}
		cfg.JoinAddr = fc.Cluster.JoinAddr
	}

	return cfg, nil
}

// resolveDisk returns the block device path to install onto. "auto" or empty
// selects the largest non-removable, non-optical disk.
func resolveDisk(target string) (string, error) {
	if target != "" && target != "auto" {
		if _, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("%q not found", target)
		}
		return target, nil
	}
	return largestNonRemovableDisk()
}

func largestNonRemovableDisk() (string, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return "", err
	}

	type candidate struct {
		dev   string
		bytes int64
	}
	var candidates []candidate

	for _, e := range entries {
		dev := e.Name()
		// Skip virtual/optical devices.
		switch {
		case strings.HasPrefix(dev, "loop"),
			strings.HasPrefix(dev, "ram"),
			strings.HasPrefix(dev, "dm-"),
			strings.HasPrefix(dev, "sr"):
			continue
		}
		removable, _ := os.ReadFile("/sys/block/" + dev + "/removable")
		if strings.TrimSpace(string(removable)) == "1" {
			continue
		}
		sizeRaw, _ := os.ReadFile("/sys/block/" + dev + "/size")
		sectors, err := strconv.ParseInt(strings.TrimSpace(string(sizeRaw)), 10, 64)
		if err != nil || sectors == 0 {
			continue
		}
		candidates = append(candidates, candidate{dev: dev, bytes: sectors * 512})
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no suitable disk found (all disks are removable or virtual)")
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].bytes > candidates[j].bytes
	})
	slog.Info("autoinstall: selected target disk", "disk", candidates[0].dev, "size_gb",
		candidates[0].bytes>>30)
	return "/dev/" + candidates[0].dev, nil
}

// resolveNIC returns the interface name to use. "auto" or empty picks the
// first physical (non-loopback) interface that is not exclude.
func resolveNIC(name, exclude string) (string, error) {
	if name != "" && name != "auto" {
		return name, nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Name == exclude {
			continue
		}
		return iface.Name, nil
	}
	return "", fmt.Errorf("no suitable NIC found")
}
