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
	return enableUnit(root)
}

func writeScript(root string, cfg Config) error {
	clusterCmd := buildClusterCmd(cfg)

	script := fmt.Sprintf(`#!/bin/bash
# Spinifex firstboot — runs once after ISO installation, then disables itself.
set -euo pipefail

# Set hostname
hostnamectl set-hostname %s

# Configure OVN networking
/usr/local/bin/spinifex-setup-ovn.sh \
  --management \
  --encap-ip=%s \
  --interface=%s

# Cluster formation
%s

# Start services
systemctl start spinifex.target

# Disable this service so it never runs again
systemctl disable spinifex-firstboot.service
`, cfg.Hostname, cfg.ManagementIP, cfg.OVNInterface, clusterCmd)

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
