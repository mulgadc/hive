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
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

// Package systemd writes systemd unit files and drop-ins into an installed
// system root during the Spinifex ISO installation process.
package systemd

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteLANBridgeUnit installs a non-critical oneshot service that brings up
// br-lan *after* network-online.target. This keeps br-lan out of
// networking.service entirely — a missing LAN cable or DHCP timeout on the
// secondary bridge can never stall the management interface or firstboot.
func WriteLANBridgeUnit(root string) error {
	unit := `[Unit]
Description=Spinifex LAN bridge (non-critical)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/sbin/ifup br-lan
RemainAfterExit=yes
# Failure is non-critical — cable unplugged or switch not ready at boot.
SuccessExitStatus=0 1

[Install]
WantedBy=multi-user.target
`
	unitPath := filepath.Join(root, "etc/systemd/system/spinifex-lan-bridge.service")
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, "spinifex-lan-bridge.service")
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale symlink %s: %w", link, err)
	}
	return os.Symlink("/etc/systemd/system/spinifex-lan-bridge.service", link)
}

// WriteNetworkingDropIn writes a networking.service drop-in that treats exit
// code 1 as success. This prevents a secondary interface failure (e.g. br-lan
// DHCP timeout when no cable is plugged in) from blocking network-online.target
// and therefore spinifex-firstboot.service.
func WriteNetworkingDropIn(root string) error {
	dir := filepath.Join(root, "etc/systemd/system/networking.service.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dropIn := "[Service]\nSuccessExitStatus=0 1\n"
	return os.WriteFile(filepath.Join(dir, "spinifex-optional-ifaces.conf"), []byte(dropIn), 0o644)
}

// WriteFirstbootUnit writes the spinifex-firstboot.service oneshot unit that
// runs the firstboot provisioning script on the first real boot after installation.
func WriteFirstbootUnit(root string) error {
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
# Cap total firstboot runtime so a hang in setup-ovn.sh / spx admin init /
# ovn-central startup cannot wedge multi-user.target and keep getty from
# ever reaching the login prompt.
TimeoutStartSec=180s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	path := filepath.Join(root, "etc/systemd/system/spinifex-firstboot.service")
	return os.WriteFile(path, []byte(unit), 0o644)
}

// WriteBannerUnit writes the spinifex-banner.service unit that runs
// `spx admin banner --boot-check` on every boot after spinifex.target is up.
// Running After=spinifex.target ensures the banner reflects a settled system
// state and that the IP check/restart happens once services are already running.
func WriteBannerUnit(root string) error {
	unit := `[Unit]
Description=Spinifex console banner and boot health check
After=spinifex.target
Wants=spinifex.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/spx admin banner --boot-check
RemainAfterExit=yes
# Banner is oneshot; cap it so a stuck boot-check (IP detection, try-restart)
# cannot block getty via the spinifex-wait.conf drop-in.
TimeoutStartSec=30s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`
	path := filepath.Join(root, "etc/systemd/system/spinifex-banner.service")
	return os.WriteFile(path, []byte(unit), 0o644)
}

// WriteGettyDropIn holds the primary consoles (tty1 and ttyS0) until
// spinifex-banner.service completes, so the MOTD banner is visible before the
// login prompt appears. Drop-ins are scoped to named instances (getty@tty1,
// serial-getty@ttyS0) so tty2, tty3, etc. remain available as rescue terminals.
func WriteGettyDropIn(root string) error {
	dropIn := `[Unit]
After=spinifex-banner.service
Wants=spinifex-banner.service
`
	for _, svc := range []string{"getty@tty1", "serial-getty@ttyS0"} {
		dropInDir := filepath.Join(root, "etc/systemd/system/"+svc+".service.d")
		if err := os.MkdirAll(dropInDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dropInDir, "spinifex-wait.conf"), []byte(dropIn), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// EnableUnit creates the multi-user.target.wants symlink for serviceName,
// equivalent to `systemctl enable` for a unit that targets multi-user.target.
func EnableUnit(root, serviceName string) error {
	wantsDir := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, serviceName)
	target := "/etc/systemd/system/" + serviceName
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale symlink %s: %w", link, err)
	}
	return os.Symlink(target, link)
}
