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

	"github.com/mulgadc/spinifex/cmd/installer/systemd"
)

// Config holds the values the firstboot service needs to configure the node.
type Config struct {
	Hostname string
	// EncapIP is the Geneve tunnel IP for OVN. Set to the LAN bridge IP when a
	// dedicated LAN NIC is present, otherwise the WAN bridge IP. Empty when DHCP
	// is used — setup-ovn.sh auto-detects the IP from the default route in that case.
	EncapIP string
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
	if err := systemd.WriteFirstbootUnit(root); err != nil {
		return fmt.Errorf("firstboot unit: %w", err)
	}
	if err := systemd.EnableUnit(root, "spinifex-firstboot.service"); err != nil {
		return err
	}
	if err := systemd.WriteBannerUnit(root); err != nil {
		return fmt.Errorf("banner unit: %w", err)
	}
	if err := systemd.EnableUnit(root, "spinifex-banner.service"); err != nil {
		return err
	}
	return systemd.WriteGettyDropIn(root)
}

func writeScript(root string, cfg Config) error {
	clusterCmd := buildClusterCmd(cfg)

	// --encap-ip is optional: when DHCP is used the IP is unknown at install time
	// and setup-ovn.sh auto-detects it from the default route at boot.
	setupOVN := "/usr/local/bin/setup-ovn.sh --management"
	if cfg.EncapIP != "" {
		setupOVN += fmt.Sprintf(" --encap-ip=%s", cfg.EncapIP)
	}

	script := fmt.Sprintf(`#!/bin/bash
# Spinifex firstboot — runs once after ISO installation, then disables itself.
set -euo pipefail

# Always disable this service on exit, even on failure, so a partial run
# does not cause an infinite retry loop on subsequent reboots.
trap 'systemctl disable spinifex-firstboot.service' EXIT

# Set hostname
hostnamectl set-hostname %s

# NOTE: We do NOT invoke /usr/local/share/spinifex/setup.sh here. The ISO
# rootfs (scripts/iso-builder/build/build-rootfs.sh) already pre-stages
# everything setup.sh would do at install time: spx binary, viperblock
# nbdkit plugin, service users (spinifex-{nats,gw,daemon,storage,viperblock,
# vpcd,ui}), data directories with per-service ownership, sudoers rules,
# systemd units, and tmpfiles.d entries. Calling setup.sh would also fail
# in the air-gapped boot environment because no tarball is staged at
# /opt/spinifex/spinifex.tar.gz — the firstboot would never complete and
# every spinifex-* service depending on it would refuse to start.

# Pre-start OVS and OVN central so their databases are initialised before
# setup-ovn.sh runs. On physical hardware, first-boot DB initialisation takes
# longer than setup-ovn.sh's internal 15-second timeout allows. Starting them
# here and waiting until the NB DB is ready means setup-ovn.sh sees a live DB
# the moment it starts — no races, no timeout failures.
systemctl start openvswitch-switch
systemctl start ovn-central
echo "Waiting for OVN NB DB to initialise..."
for _i in $(seq 1 120); do
    if ovn-nbctl --timeout=2 get-connection >/dev/null 2>&1; then
        echo "OVN NB DB ready (${_i}s)"
        break
    fi
    sleep 1
done

# Configure OVN networking.
# br-wan (and br-lan if present) are Linux bridges created by the installer.
# setup-ovn.sh auto-detects br-wan as the default route device (Linux bridge)
# and wires it to OVS via a veth pair — non-destructive, SSH-safe.
%s

# Cluster formation — capture credentials to file for display on console.
%s 2>&1

# Fix ownership of files spx admin init created. spx runs as root (no
# SUDO_USER under systemd), so /etc/spinifex/{spinifex.toml,master.key} and
# any per-service files written under /var/lib/spinifex/* land as root:root.
# Re-chown them so each service can read/write its own subtree at
# Type=notify start. The directories themselves were pre-created with the
# correct ownership in build-rootfs.sh — these chowns only affect the
# runtime-generated config and key files inside them.
chown root:spinifex /etc/spinifex && chmod 750 /etc/spinifex
find /etc/spinifex -type f -exec chmod 640 {} \;
chown -R spinifex-gw:spinifex         /var/lib/spinifex/awsgw
chown -R spinifex-daemon:spinifex     /var/lib/spinifex/spinifex
chown -R spinifex-nats:spinifex       /var/lib/spinifex/nats
chown -R spinifex-storage:spinifex    /var/lib/spinifex/predastore
chown -R spinifex-viperblock:spinifex /var/lib/spinifex/viperblock
chown -R spinifex-vpcd:spinifex       /var/lib/spinifex/vpcd

# awsgw looks for master.key at <BaseDir>/config/master.key. In production
# BaseDir is /var/lib/spinifex/awsgw/ (set by SPINIFEX_BASE_DIR), but the
# key lives in /etc/spinifex/. Symlink so both paths resolve to the same
# file. spx admin init writes the key, so this must run after it.
mkdir -p /var/lib/spinifex/awsgw/config
ln -sf /etc/spinifex/master.key /var/lib/spinifex/awsgw/config/master.key

# Copy AWS credentials to the spinifex user's home directory.
# spx admin init runs with HOME=/root (set by the systemd unit), so credentials
# land in /root/.aws/. Copy them to the spinifex user's home so the operator
# can use the AWS CLI without sudo.
if [ -f /root/.aws/credentials ]; then
    mkdir -p /home/spinifex/.aws
    cp /root/.aws/credentials /home/spinifex/.aws/credentials
    cp /root/.aws/config /home/spinifex/.aws/config 2>/dev/null || true
    chown -R spinifex:spinifex /home/spinifex/.aws
    chmod 700 /home/spinifex/.aws
    chmod 600 /home/spinifex/.aws/credentials
    [ -f /home/spinifex/.aws/config ] && chmod 600 /home/spinifex/.aws/config
fi

# Start services
systemctl start spinifex.target
`, cfg.Hostname, setupOVN, clusterCmd)

	path := filepath.Join(root, "usr/local/bin/spinifex-firstboot.sh")
	return os.WriteFile(path, []byte(script), 0o755)
}

func buildClusterCmd(cfg Config) string {
	switch cfg.ClusterRole {
	case "join":
		return fmt.Sprintf("spx admin join --node %s --host %s", cfg.Hostname, cfg.JoinAddr)
	default:
		return fmt.Sprintf("spx admin init --node %s --nodes 1", cfg.Hostname)
	}
}
