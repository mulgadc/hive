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

# Run setup.sh with the embedded tarball (air-gapped, idempotent).
# This creates service users, directories, sudoers rules, installs and enables
# the systemd units — the same path as the online curl|bash installer.
# Apt deps and AWS CLI are pre-installed in the squashfs so both are skipped.
INSTALL_SPINIFEX_TARBALL=/opt/spinifex/spinifex.tar.gz \
INSTALL_SPINIFEX_SKIP_APT=1 \
INSTALL_SPINIFEX_SKIP_AWS=1 \
INSTALL_SPINIFEX_SKIP_NEWGRP=1 \
bash /usr/local/share/spinifex/setup.sh

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
