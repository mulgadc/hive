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

// Package install performs the disk installation steps after the UI has
// collected configuration.
package install

// Config holds all values collected by the installer UI.
type Config struct {
	// Disk is the block device path to install onto (e.g. /dev/sda).
	Disk string

	// Network
	ManagementIP string
	SubnetMask   string
	Gateway      string
	OVNInterface string

	// Node identity
	Hostname string

	// ClusterRole is "init" or "join".
	ClusterRole string

	// JoinAddr is the primary node address (host:port) when ClusterRole is "join".
	JoinAddr string

	// CA certificate (PEM), optional.
	HasCACert bool
	CACert    string
}
