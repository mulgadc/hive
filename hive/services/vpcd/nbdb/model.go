// Package nbdb contains Go structs representing the OVN Northbound Database schema.
// These models are used with libovsdb to interact with the OVN NB DB.
//
// The structs cover the core tables needed for Hive VPC networking:
// LogicalSwitch, LogicalSwitchPort, LogicalRouter, LogicalRouterPort, and DHCPOptions.
//
// To regenerate from the full OVN NB schema (requires OVN installed):
//
//	go install github.com/ovn-kubernetes/libovsdb/cmd/modelgen@latest
//	modelgen -p nbdb -o hive/services/vpcd/nbdb /usr/share/ovn/ovn-nb.ovsschema
package nbdb

import "github.com/ovn-kubernetes/libovsdb/model"

// LogicalSwitch represents an OVN Logical_Switch (L2 segment, maps to a subnet).
type LogicalSwitch struct {
	UUID        string            `ovsdb:"_uuid"`
	Name        string            `ovsdb:"name"`
	Ports       []string          `ovsdb:"ports"`
	ACLs        []string          `ovsdb:"acls"`
	DNSRecords  []string          `ovsdb:"dns_records"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	OtherConfig map[string]string `ovsdb:"other_config"`
}

// LogicalSwitchPort represents an OVN Logical_Switch_Port (VM port / ENI).
type LogicalSwitchPort struct {
	UUID          string            `ovsdb:"_uuid"`
	Name          string            `ovsdb:"name"`
	Type          string            `ovsdb:"type"`
	Addresses     []string          `ovsdb:"addresses"`
	PortSecurity  []string          `ovsdb:"port_security"`
	DHCPv4Options *string           `ovsdb:"dhcpv4_options"`
	Enabled       *bool             `ovsdb:"enabled"`
	Up            *bool             `ovsdb:"up"`
	ExternalIDs   map[string]string `ovsdb:"external_ids"`
	Options       map[string]string `ovsdb:"options"`
}

// LogicalRouter represents an OVN Logical_Router (VPC router).
type LogicalRouter struct {
	UUID         string            `ovsdb:"_uuid"`
	Name         string            `ovsdb:"name"`
	Ports        []string          `ovsdb:"ports"`
	StaticRoutes []string          `ovsdb:"static_routes"`
	NAT          []string          `ovsdb:"nat"`
	Policies     []string          `ovsdb:"policies"`
	Enabled      *bool             `ovsdb:"enabled"`
	ExternalIDs  map[string]string `ovsdb:"external_ids"`
	Options      map[string]string `ovsdb:"options"`
}

// LogicalRouterPort represents an OVN Logical_Router_Port.
type LogicalRouterPort struct {
	UUID           string            `ovsdb:"_uuid"`
	Name           string            `ovsdb:"name"`
	MAC            string            `ovsdb:"mac"`
	Networks       []string          `ovsdb:"networks"`
	GatewayChassis []string          `ovsdb:"gateway_chassis"`
	ExternalIDs    map[string]string `ovsdb:"external_ids"`
	Options        map[string]string `ovsdb:"options"`
}

// DHCPOptions represents an OVN DHCP_Options row.
type DHCPOptions struct {
	UUID        string            `ovsdb:"_uuid"`
	CIDR        string            `ovsdb:"cidr"`
	Options     map[string]string `ovsdb:"options"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
}

// FullDatabaseModel returns a ClientDBModel for the OVN Northbound database
// containing all tables needed for Hive VPC networking.
func FullDatabaseModel() (model.ClientDBModel, error) {
	return model.NewClientDBModel("OVN_Northbound", map[string]model.Model{
		"Logical_Switch":      &LogicalSwitch{},
		"Logical_Switch_Port": &LogicalSwitchPort{},
		"Logical_Router":      &LogicalRouter{},
		"Logical_Router_Port": &LogicalRouterPort{},
		"DHCP_Options":        &DHCPOptions{},
	})
}
