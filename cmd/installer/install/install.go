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

package install

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mulgadc/spinifex/cmd/installer/firstboot"
)

const (
	mountRoot = "/mnt/spinifex-install"
	efiPart   = mountRoot + "/boot/efi"
)

// Run executes all installation steps in order. It is intentionally sequential
// and explicit — each step is visible in logs so failures are easy to diagnose.
func Run(cfg *Config) error {
	// The live environment may not have /sbin or /usr/sbin in PATH. Set it
	// explicitly so exec.Command's LookPath finds system binaries like grub-install.
	_ = os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	steps := []struct {
		name string
		fn   func() error
	}{
		{"partition disk", func() error { return partitionDisk(cfg.Disk) }},
		{"format partitions", func() error { return formatPartitions(cfg.Disk) }},
		{"mount partitions", func() error { return mountPartitions(cfg.Disk) }},
		{"copy rootfs", copyRootfs},
		{"write fstab", func() error { return writeFstab(cfg.Disk) }},
		{"install spinifex", func() error { return installSpinifex(cfg) }},
		{"write network config", func() error { return writeNetworkConfig(cfg) }},
		{"write firstboot service", func() error { return firstboot.Write(mountRoot, cfg.toFirstbootConfig()) }},
		{"install bootloader", func() error { return installBootloader(cfg.Disk) }},
		{"install CA cert", func() error { return installCACert(cfg) }},
		{"unmount", unmount},
	}

	for _, s := range steps {
		slog.Info("installer", "step", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("step %q: %w", s.name, err)
		}
	}

	slog.Info("installation complete — rebooting")
	return reboot()
}

func partitionDisk(disk string) error {
	// GPT table with three partitions:
	//   p1: 1MiB BIOS Boot Partition — required for grub-install i386-pc on GPT
	//   p2: 512MiB EFI System Partition
	//   p3: remainder as root (ext4)
	return run("parted", "--script", disk,
		"mklabel", "gpt",
		"mkpart", "bios_boot", "1MiB", "2MiB",
		"set", "1", "bios_grub", "on",
		"mkpart", "ESP", "fat32", "2MiB", "514MiB",
		"set", "2", "esp", "on",
		"mkpart", "root", "ext4", "514MiB", "100%",
	)
}

func formatPartitions(disk string) error {
	efi, root := partitionPaths(disk)
	if err := run("mkfs.fat", "-F32", efi); err != nil {
		return err
	}
	return run("mkfs.ext4", "-F", root)
}

func mountPartitions(disk string) error {
	_, root := partitionPaths(disk)
	if err := os.MkdirAll(mountRoot, 0o755); err != nil {
		return err
	}
	if err := run("mount", root, mountRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(efiPart, 0o755); err != nil {
		return err
	}
	efi, _ := partitionPaths(disk)
	return run("mount", efi, efiPart)
}

// copyRootfs copies the live squashfs environment onto the target disk using
// rsync. This is the air-gapped alternative to debootstrap — all packages are
// already embedded in the ISO so no network access is required.
func copyRootfs() error {
	if err := run("rsync", "-aHAX", "--delete",
		"--exclude=/proc",
		"--exclude=/sys",
		"--exclude=/dev",
		"--exclude=/run",
		"--exclude=/tmp",
		"--exclude=/cdrom",
		"--exclude=/mnt",
		"--exclude=/lost+found",
		"--exclude=/boot/efi",
		"/", mountRoot+"/",
	); err != nil {
		return err
	}

	// rsync skips excluded paths entirely — recreate the virtual filesystem
	// mount points that systemd expects to exist on the installed system.
	mountPoints := []struct {
		path string
		mode os.FileMode
	}{
		{"proc", 0o555},
		{"sys", 0o555},
		{"dev", 0o755},
		{"run", 0o755},
		{"run/lock", 0o1777},
		{"tmp", 0o1777},
		{"mnt", 0o755},
	}
	for _, mp := range mountPoints {
		if err := os.MkdirAll(filepath.Join(mountRoot, mp.path), mp.mode); err != nil {
			return fmt.Errorf("create mountpoint /%s: %w", mp.path, err)
		}
	}
	return nil
}

func installSpinifex(cfg *Config) error {
	// The rootfs copy already contains spx and spinifex-installer at
	// /usr/local/bin/ — no binary copy needed. Regenerate machine-specific
	// identity files so each installed node is unique.

	// Fresh machine-id (required by systemd and dbus).
	machineIDPath := filepath.Join(mountRoot, "etc/machine-id")
	_ = os.Remove(machineIDPath)
	if err := run("chroot", mountRoot, "systemd-machine-id-setup"); err != nil {
		// Fallback: write empty file so systemd generates one on first boot.
		_ = os.WriteFile(machineIDPath, []byte(""), 0o444)
	}

	// Hostname.
	hostnamePath := filepath.Join(mountRoot, "etc/hostname")
	if err := os.WriteFile(hostnamePath, []byte(cfg.Hostname+"\n"), 0o644); err != nil {
		return err
	}

	// /etc/hosts entry for the hostname.
	hosts := fmt.Sprintf("127.0.0.1\tlocalhost\n127.0.1.1\t%s\n", cfg.Hostname)
	if err := os.WriteFile(filepath.Join(mountRoot, "etc/hosts"), []byte(hosts), 0o644); err != nil {
		return err
	}

	// Set root password via chpasswd inside the chroot.
	if cfg.RootPassword != "" {
		cmd := exec.Command("chroot", mountRoot, "chpasswd")
		cmd.Stdin = strings.NewReader("root:" + cfg.RootPassword)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("set root password: %w", err)
		}
	}

	// Write /etc/spinifex/node.conf — read at runtime by spinifex-banner.sh
	// to look up the current IP dynamically (handles IP changes after install).
	nodeConf := fmt.Sprintf("MANAGEMENT_IP=%s\nMANAGEMENT_IFACE=%s\nNODE_HOSTNAME=%s\n",
		cfg.ManagementIP, cfg.OVNInterface, cfg.Hostname)
	confDir := filepath.Join(mountRoot, "etc/spinifex")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(confDir, "node.conf"), []byte(nodeConf), 0o644)
}

func writeNetworkConfig(cfg *Config) error {
	// Write a simple /etc/network/interfaces for the management interface.
	// The OVN interface is left unconfigured here — setup-ovn.sh handles it
	// on first boot.
	content := fmt.Sprintf(`# Generated by Spinifex installer
auto lo
iface lo inet loopback

auto %s
iface %s inet static
    address %s
    netmask %s
    gateway %s
`, cfg.OVNInterface, cfg.OVNInterface, cfg.ManagementIP, cfg.SubnetMask, cfg.Gateway)

	if err := os.WriteFile(filepath.Join(mountRoot, "etc/network/interfaces"), []byte(content), 0o644); err != nil {
		return err
	}

	// Pin the NIC name to its MAC address via a udev rule so the installed
	// system always uses the same name regardless of the host's udev naming
	// policy (predictable names vs eth0-style). Robust across hardware.
	mac, err := os.ReadFile("/sys/class/net/" + cfg.OVNInterface + "/address")
	if err != nil {
		slog.Warn("writeNetworkConfig: could not read NIC MAC, skipping udev pin", "iface", cfg.OVNInterface, "err", err)
		return nil
	}
	udevRule := fmt.Sprintf("SUBSYSTEM==\"net\", ACTION==\"add\", ATTR{address}==\"%s\", NAME=\"%s\"\n",
		strings.TrimSpace(string(mac)), cfg.OVNInterface)
	udevDir := filepath.Join(mountRoot, "etc/udev/rules.d")
	if err := os.MkdirAll(udevDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(udevDir, "70-spinifex-net.rules"), []byte(udevRule), 0o644)
}

func installBootloader(disk string) error {
	// grub-install runs in the live environment (not chroot) using the
	// grub-pc-bin and grub-efi-amd64-bin packages already present on the ISO.
	// --boot-directory points at the installed system's /boot.
	bootDir := filepath.Join(mountRoot, "boot")
	efiDir := filepath.Join(mountRoot, "boot", "efi")

	if err := run("grub-install",
		"--target=x86_64-efi",
		"--efi-directory="+efiDir,
		"--boot-directory="+bootDir,
		"--bootloader-id=spinifex",
		"--removable",
		"--recheck",
	); err != nil {
		slog.Warn("installBootloader: EFI install failed (may not be EFI system)", "err", err)
	}
	if err := run("grub-install",
		"--target=i386-pc",
		"--boot-directory="+bootDir,
		"--recheck",
		disk,
	); err != nil {
		return err
	}
	// Configure serial console in the installed system's GRUB so the boot menu
	// and kernel output are visible over serial (ttyS0) as well as VGA.
	grubDefault := `GRUB_DEFAULT=0
GRUB_TIMEOUT=5
GRUB_DISTRIBUTOR=Spinifex
GRUB_CMDLINE_LINUX_DEFAULT=""
GRUB_CMDLINE_LINUX="console=tty0 console=ttyS0,115200n8 systemd.show_status=1"
GRUB_TERMINAL="serial console"
GRUB_SERIAL_COMMAND="serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1"
`
	if err := os.WriteFile(filepath.Join(mountRoot, "etc/default/grub"), []byte(grubDefault), 0o644); err != nil {
		return fmt.Errorf("write /etc/default/grub: %w", err)
	}

	// update-grub (grub-mkconfig) runs inside the installed system's chroot and
	// needs /dev, /proc, and /sys to probe devices. Bind-mount them in and
	// unmount regardless of outcome.
	chrootMounts := []string{"dev", "proc", "sys"}
	for _, m := range chrootMounts {
		dst := filepath.Join(mountRoot, m)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("create chroot mountpoint /%s: %w", m, err)
		}
		if err := run("mount", "--bind", "/"+m, dst); err != nil {
			return fmt.Errorf("bind-mount /%s into chroot: %w", m, err)
		}
	}
	defer func() {
		// Unmount in reverse order; ignore errors (best-effort cleanup).
		for i := len(chrootMounts) - 1; i >= 0; i-- {
			_ = run("umount", filepath.Join(mountRoot, chrootMounts[i]))
		}
	}()
	return run("chroot", mountRoot, "update-grub")
}

func installCACert(cfg *Config) error {
	if !cfg.HasCACert || cfg.CACert == "" {
		return nil
	}
	certPath := filepath.Join(mountRoot, "usr/local/share/ca-certificates/spinifex-ca.crt")
	if err := os.WriteFile(certPath, []byte(cfg.CACert), 0o644); err != nil {
		return err
	}
	return run("chroot", mountRoot, "update-ca-certificates")
}

func unmount() error {
	if err := run("umount", efiPart); err != nil {
		return err
	}
	return run("umount", mountRoot)
}

func reboot() error {
	// sync filesystems before reboot so nothing is lost.
	_ = run("sync")
	// Use the kernel syscall directly — the live environment runs spinifex-init
	// as PID 1 (not systemd), so the reboot(8) utility fails trying to reach D-Bus.
	return syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
}

// toFirstbootConfig maps installer Config to the firstboot package's Config.
func (c *Config) toFirstbootConfig() firstboot.Config {
	return firstboot.Config{
		Hostname:     c.Hostname,
		OVNInterface: c.OVNInterface,
		ManagementIP: c.ManagementIP,
		ClusterRole:  c.ClusterRole,
		JoinAddr:     c.JoinAddr,
	}
}

// writeFstab writes /etc/fstab on the installed system using partition UUIDs so
// the root filesystem is mounted read-write at boot and the EFI partition is
// mounted at /boot/efi.
func writeFstab(disk string) error {
	efi, root := partitionPaths(disk)
	rootUUID, err := partUUID(root)
	if err != nil {
		return fmt.Errorf("get root UUID: %w", err)
	}
	efiUUID, err := partUUID(efi)
	if err != nil {
		return fmt.Errorf("get EFI UUID: %w", err)
	}
	fstab := fmt.Sprintf("# /etc/fstab — generated by Spinifex installer\nUUID=%s / ext4 errors=remount-ro 0 1\nUUID=%s /boot/efi vfat umask=0077 0 1\n",
		rootUUID, efiUUID)
	return os.WriteFile(filepath.Join(mountRoot, "etc/fstab"), []byte(fstab), 0o644)
}

func partUUID(dev string) (string, error) {
	out, err := exec.Command("blkid", "-s", "UUID", "-o", "value", dev).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// partitionPaths returns the EFI and root partition device paths for a given
// disk. p1 is the BIOS Boot Partition (no filesystem), p2 is EFI, p3 is root.
// Handles both /dev/sdX (→ /dev/sdX2, /dev/sdX3) and /dev/nvmeXnY
// (→ /dev/nvmeXnYp2, /dev/nvmeXnYp3).
func partitionPaths(disk string) (efi, root string) {
	// NVMe devices use a 'p' separator before the partition number.
	if len(disk) > 0 && disk[len(disk)-1] >= '0' && disk[len(disk)-1] <= '9' {
		return disk + "p2", disk + "p3"
	}
	return disk + "2", disk + "3"
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
