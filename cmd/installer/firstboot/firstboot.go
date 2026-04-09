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

// Package firstboot writes the oneshot systemd service and configuration that
// completes Spinifex provisioning on the first real boot after installation.
package firstboot

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the values the firstboot service needs to configure the node.
type Config struct {
	Hostname     string
	OVNInterface string
	ManagementIP string
	// ClusterRole is "init" or "join".
	ClusterRole string
	// JoinAddr is host:port of the primary node, only used when ClusterRole is "join".
	JoinAddr string
}

// Write drops the firstboot script and systemd unit into root, which should be
// the path of the installed system's root filesystem (e.g. /mnt/spinifex-install).
func Write(root string, cfg Config) error {
	if err := writeScript(root, cfg); err != nil {
		return fmt.Errorf("firstboot script: %w", err)
	}
	if err := writeUnit(root); err != nil {
		return fmt.Errorf("firstboot unit: %w", err)
	}
	if err := enableUnit(root); err != nil {
		return err
	}
	if err := writeBannerScript(root, cfg); err != nil {
		return fmt.Errorf("banner script: %w", err)
	}
	if err := writeBannerUnit(root); err != nil {
		return fmt.Errorf("banner unit: %w", err)
	}
	return enableBannerUnit(root)
}

func writeScript(root string, cfg Config) error {
	clusterCmd := buildClusterCmd(cfg)

	script := fmt.Sprintf(`#!/bin/bash
# Spinifex firstboot — runs once after ISO installation, then disables itself.
set -euo pipefail

# Set hostname
hostnamectl set-hostname %s

# Configure OVN networking.
# --macvlan creates a virtual sub-interface (spx-ext-<NIC>) off the management
# NIC so OVN gets its own L2 path without stealing the host IP (SSH-safe).
/usr/local/bin/spinifex-setup-ovn.sh \
  --management \
  --macvlan \
  --wan-iface=%s \
  --encap-ip=%s

# Cluster formation — capture credentials to file for display on console.
%s 2>&1 | tee /root/spinifex-credentials.txt
chmod 600 /root/spinifex-credentials.txt

# Fix ownership: spx admin init runs as root (no SUDO_USER in systemd context)
# so config and data files are created as root:root. Hand them to the service
# user so systemd units running as spinifex can read them.
chown -R spinifex:spinifex /etc/spinifex /var/lib/spinifex

# Start services
systemctl start spinifex.target

# Disable this service so it never runs again
systemctl disable spinifex-firstboot.service
`, cfg.Hostname, cfg.OVNInterface, cfg.ManagementIP, clusterCmd)

	path := filepath.Join(root, "usr/local/bin/spinifex-firstboot.sh")
	return os.WriteFile(path, []byte(script), 0o755)
}

func writeUnit(root string) error {
	unit := `[Unit]
Description=Spinifex first-boot provisioning
After=network-online.target
Wants=network-online.target
ConditionPathExists=/usr/local/bin/spinifex-firstboot.sh

[Service]
Type=oneshot
Environment=HOME=/root
ExecStart=/usr/local/bin/spinifex-firstboot.sh
RemainAfterExit=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	path := filepath.Join(root, "etc/systemd/system/spinifex-firstboot.service")
	return os.WriteFile(path, []byte(unit), 0o644)
}

func enableUnit(root string) error {
	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, "spinifex-firstboot.service")
	target := "/etc/systemd/system/spinifex-firstboot.service"
	// Remove stale symlink if present.
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

func buildClusterCmd(cfg Config) string {
	switch cfg.ClusterRole {
	case "join":
		return fmt.Sprintf("spx admin join --node %s --host %s", cfg.Hostname, cfg.JoinAddr)
	default:
		return fmt.Sprintf("spx admin init --node %s --nodes 1", cfg.Hostname)
	}
}

func writeBannerScript(root string, _ Config) error {
	// The banner reads IP and interface at runtime from /etc/spinifex/node.conf
	// so it stays correct if the operator changes the IP after install.
	script := `#!/bin/bash
# Spinifex console banner — printed to tty1 on every boot after services start.

# Load node config written by the installer.
# shellcheck source=/etc/spinifex/node.conf
. /etc/spinifex/node.conf 2>/dev/null || true

# Resolve the current IP from the management interface at runtime.
IP=""
if [ -n "${MANAGEMENT_IFACE:-}" ]; then
    IP=$(ip -4 addr show "$MANAGEMENT_IFACE" 2>/dev/null \
        | awk '/inet /{gsub(/\/.*/, "", $2); print $2; exit}')
fi
# Fall back to the IP recorded at install time.
IP="${IP:-${MANAGEMENT_IP:-<unknown>}}"
HOST="${NODE_HOSTNAME:-$(hostname)}"

{
    echo ""
    echo "  +----------------------------------------------------+"
    echo "  |         Spinifex  —  Mulga Defense Corporation     |"
    echo "  +----------------------------------------------------+"
    printf "  |  Node:      %-40s|\n" "$HOST"
    printf "  |  Dashboard: %-40s|\n" "https://$IP:3000"
    printf "  |  API:       %-40s|\n" "https://$IP:9999"
    printf "  |  SSH:       %-40s|\n" "root@$IP"
    echo "  +----------------------------------------------------+"
    echo "  |  Login credentials (AWS Access Key + Secret Key)   |"
    echo "  |    cat /root/spinifex-credentials.txt              |"
    if [ ! -f /root/spinifex-credentials.txt ]; then
        echo "  |  (credentials file not found — check firstboot)    |"
    fi
    echo "  +----------------------------------------------------+"
    echo ""
} | tee /dev/tty1 > /etc/motd
`

	path := filepath.Join(root, "usr/local/bin/spinifex-banner.sh")
	return os.WriteFile(path, []byte(script), 0o755)
}

func writeBannerUnit(root string) error {
	unit := `[Unit]
Description=Spinifex console banner
After=spinifex.target
Wants=spinifex.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/spinifex-banner.sh
RemainAfterExit=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	path := filepath.Join(root, "etc/systemd/system/spinifex-banner.service")
	return os.WriteFile(path, []byte(unit), 0o644)
}

func enableBannerUnit(root string) error {
	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, "spinifex-banner.service")
	target := "/etc/systemd/system/spinifex-banner.service"
	_ = os.Remove(link)
	return os.Symlink(target, link)
}
