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

// Package ui presents the interactive installer forms using huh.
package ui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/mulgadc/spinifex/cmd/installer/install"
)

// Run walks the operator through all installer forms and returns a completed
// Config ready to hand to install.Run.
func Run() (*install.Config, error) {
	cfg := &install.Config{}

	disks, err := availableDisks()
	if err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}
	if len(disks) == 0 {
		return nil, errors.New("no block devices found")
	}

	nics, err := availableNICs()
	if err != nil {
		return nil, fmt.Errorf("listing network interfaces: %w", err)
	}

	var joinHost string
	var joinPort string
	var caCert string

	form := huh.NewForm(
		// Step 1: Welcome
		huh.NewGroup(
			huh.NewNote().
				Title("Spinifex Installer").
				Description("This will install Spinifex onto this machine.\n\nAll data on the selected disk will be erased.\nPress Enter to continue."),
		),

		// Step 2: Disk selection
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select installation disk").
				Description("All data on the selected disk will be permanently erased.").
				Options(diskOptions(disks)...).
				Value(&cfg.Disk),
			huh.NewConfirm().
				Title("Confirm disk erasure").
				Description("Are you sure? This cannot be undone.").
				Affirmative("Yes, erase and install").
				Negative("Cancel").
				Validate(func(v bool) error {
					if !v {
						return errors.New("installation cancelled")
					}
					return nil
				}).
				Value(new(bool)),
		),

		// Step 3: Network configuration
		huh.NewGroup(
			huh.NewInput().
				Title("Management IP address").
				Description("Static IP address for this node (e.g. 192.168.1.10)").
				Validate(validateIP).
				Value(&cfg.ManagementIP),
			huh.NewInput().
				Title("Subnet mask").
				Placeholder("255.255.255.0").
				Validate(validateIP).
				Value(&cfg.SubnetMask),
			huh.NewInput().
				Title("Default gateway").
				Validate(validateIP).
				Value(&cfg.Gateway),
			huh.NewSelect[string]().
				Title("OVN network interface").
				Description("Physical NIC to use for VM networking").
				Options(nicOptions(nics)...).
				Value(&cfg.OVNInterface),
		),

		// Step 4: Node configuration
		huh.NewGroup(
			huh.NewInput().
				Title("Hostname").
				Description("Name for this node (e.g. node1)").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("hostname is required")
					}
					return nil
				}).
				Value(&cfg.Hostname),
			huh.NewSelect[string]().
				Title("Cluster role").
				Options(
					huh.NewOption("Initialize new cluster (single or first node)", "init"),
					huh.NewOption("Join existing cluster", "join"),
				).
				Value(&cfg.ClusterRole),
		),

		// Step 5: Join configuration (shown when joining)
		huh.NewGroup(
			huh.NewInput().
				Title("Primary node IP").
				Description("IP address of the node running spx admin init").
				Validate(func(s string) error {
					if cfg.ClusterRole != "join" {
						return nil
					}
					return validateIP(s)
				}).
				Value(&joinHost),
			huh.NewInput().
				Title("Formation port").
				Placeholder("4432").
				Value(&joinPort),
		).WithHideFunc(func() bool {
			return cfg.ClusterRole != "join"
		}),

		// Step 6: Optional CA certificate
		huh.NewGroup(
			huh.NewConfirm().
				Title("Install a custom CA certificate?").
				Description("Required for air-gapped deployments with a private certificate authority.").
				Affirmative("Yes").
				Negative("No").
				Value(&cfg.HasCACert),
		),
		huh.NewGroup(
			huh.NewText().
				Title("CA certificate (PEM format)").
				Description("Paste the full PEM certificate block.").
				Validate(func(s string) error {
					if !cfg.HasCACert {
						return nil
					}
					if !strings.Contains(s, "BEGIN CERTIFICATE") {
						return errors.New("does not look like a PEM certificate")
					}
					return nil
				}).
				Value(&caCert),
		).WithHideFunc(func() bool {
			return !cfg.HasCACert
		}),

		// Step 7: Final confirmation
		huh.NewGroup(
			huh.NewConfirm().
				Title("Begin installation?").
				Description("Review your choices above. Installation will begin immediately.").
				Affirmative("Install").
				Negative("Cancel").
				Validate(func(v bool) error {
					if !v {
						return errors.New("installation cancelled")
					}
					return nil
				}).
				Value(new(bool)),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	if cfg.ClusterRole == "join" {
		port := strings.TrimSpace(joinPort)
		if port == "" {
			port = "4432"
		}
		cfg.JoinAddr = net.JoinHostPort(strings.TrimSpace(joinHost), port)
	}

	cfg.CACert = strings.TrimSpace(caCert)

	return cfg, nil
}

func validateIP(s string) error {
	if net.ParseIP(strings.TrimSpace(s)) == nil {
		return errors.New("invalid IP address")
	}
	return nil
}

// availableDisks returns block device paths from /sys/block, excluding loop
// and RAM devices.
func availableDisks() ([]string, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}
	var disks []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		disks = append(disks, "/dev/"+name)
	}
	return disks, nil
}

func diskOptions(disks []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(disks))
	for i, d := range disks {
		opts[i] = huh.NewOption(d, d)
	}
	return opts
}

// availableNICs returns non-loopback network interface names.
func availableNICs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var nics []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		nics = append(nics, iface.Name)
	}
	return nics, nil
}

func nicOptions(nics []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(nics))
	for i, n := range nics {
		opts[i] = huh.NewOption(n, n)
	}
	return opts
}
