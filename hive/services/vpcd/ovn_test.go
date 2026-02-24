package vpcd

import (
	"context"
	"testing"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
)

func TestMockOVNClient_Connect(t *testing.T) {
	mock := NewMockOVNClient()
	if mock.Connected() {
		t.Fatal("expected not connected before Connect()")
	}
	if err := mock.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if !mock.Connected() {
		t.Fatal("expected connected after Connect()")
	}
	mock.Close()
	if mock.Connected() {
		t.Fatal("expected not connected after Close()")
	}
}

func TestMockOVNClient_LogicalSwitch_CRUD(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Create
	ls := &nbdb.LogicalSwitch{
		Name:        "subnet-test",
		ExternalIDs: map[string]string{"subnet-id": "subnet-001"},
	}
	if err := mock.CreateLogicalSwitch(ctx, ls); err != nil {
		t.Fatalf("CreateLogicalSwitch failed: %v", err)
	}

	// Duplicate should fail
	if err := mock.CreateLogicalSwitch(ctx, ls); err == nil {
		t.Fatal("expected error on duplicate create")
	}

	// Get
	got, err := mock.GetLogicalSwitch(ctx, "subnet-test")
	if err != nil {
		t.Fatalf("GetLogicalSwitch failed: %v", err)
	}
	if got.Name != "subnet-test" {
		t.Fatalf("expected name subnet-test, got %s", got.Name)
	}
	if got.ExternalIDs["subnet-id"] != "subnet-001" {
		t.Fatal("external_ids not preserved")
	}

	// List
	switches, err := mock.ListLogicalSwitches(ctx)
	if err != nil {
		t.Fatalf("ListLogicalSwitches failed: %v", err)
	}
	if len(switches) != 1 {
		t.Fatalf("expected 1 switch, got %d", len(switches))
	}

	// Delete
	if err := mock.DeleteLogicalSwitch(ctx, "subnet-test"); err != nil {
		t.Fatalf("DeleteLogicalSwitch failed: %v", err)
	}

	// Get after delete should fail
	if _, err := mock.GetLogicalSwitch(ctx, "subnet-test"); err == nil {
		t.Fatal("expected error after delete")
	}

	// Delete non-existent should fail
	if err := mock.DeleteLogicalSwitch(ctx, "no-such-switch"); err == nil {
		t.Fatal("expected error deleting non-existent switch")
	}
}

func TestMockOVNClient_LogicalSwitchPort_CRUD(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Create switch first
	ls := &nbdb.LogicalSwitch{Name: "subnet-001"}
	if err := mock.CreateLogicalSwitch(ctx, ls); err != nil {
		t.Fatalf("CreateLogicalSwitch failed: %v", err)
	}

	// Create port
	lsp := &nbdb.LogicalSwitchPort{
		Name:         "port-eni-001",
		Addresses:    []string{"00:00:00:00:01:01 10.0.1.11"},
		PortSecurity: []string{"00:00:00:00:01:01 10.0.1.11"},
		ExternalIDs:  map[string]string{"eni-id": "eni-001"},
	}
	if err := mock.CreateLogicalSwitchPort(ctx, "subnet-001", lsp); err != nil {
		t.Fatalf("CreateLogicalSwitchPort failed: %v", err)
	}

	// Port should be in switch's ports list
	sw, _ := mock.GetLogicalSwitch(ctx, "subnet-001")
	if len(sw.Ports) != 1 {
		t.Fatalf("expected 1 port in switch, got %d", len(sw.Ports))
	}

	// Get port
	got, err := mock.GetLogicalSwitchPort(ctx, "port-eni-001")
	if err != nil {
		t.Fatalf("GetLogicalSwitchPort failed: %v", err)
	}
	if got.Name != "port-eni-001" {
		t.Fatalf("expected port name port-eni-001, got %s", got.Name)
	}
	if len(got.Addresses) != 1 || got.Addresses[0] != "00:00:00:00:01:01 10.0.1.11" {
		t.Fatal("addresses not preserved")
	}

	// Update port
	got.PortSecurity = []string{"00:00:00:00:01:01 10.0.1.12"}
	if err := mock.UpdateLogicalSwitchPort(ctx, got); err != nil {
		t.Fatalf("UpdateLogicalSwitchPort failed: %v", err)
	}
	updated, _ := mock.GetLogicalSwitchPort(ctx, "port-eni-001")
	if len(updated.PortSecurity) != 1 || updated.PortSecurity[0] != "00:00:00:00:01:01 10.0.1.12" {
		t.Fatal("port security not updated")
	}

	// Create port on non-existent switch should fail
	if err := mock.CreateLogicalSwitchPort(ctx, "no-such-switch", &nbdb.LogicalSwitchPort{Name: "port-x"}); err == nil {
		t.Fatal("expected error creating port on non-existent switch")
	}

	// Delete port
	if err := mock.DeleteLogicalSwitchPort(ctx, "subnet-001", "port-eni-001"); err != nil {
		t.Fatalf("DeleteLogicalSwitchPort failed: %v", err)
	}

	sw, _ = mock.GetLogicalSwitch(ctx, "subnet-001")
	if len(sw.Ports) != 0 {
		t.Fatalf("expected 0 ports in switch after delete, got %d", len(sw.Ports))
	}
}

func TestMockOVNClient_LogicalRouter_CRUD(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Create
	lr := &nbdb.LogicalRouter{
		Name:        "vpc-001",
		ExternalIDs: map[string]string{"vpc-id": "vpc-001"},
	}
	if err := mock.CreateLogicalRouter(ctx, lr); err != nil {
		t.Fatalf("CreateLogicalRouter failed: %v", err)
	}

	// Duplicate should fail
	if err := mock.CreateLogicalRouter(ctx, lr); err == nil {
		t.Fatal("expected error on duplicate create")
	}

	// Get
	got, err := mock.GetLogicalRouter(ctx, "vpc-001")
	if err != nil {
		t.Fatalf("GetLogicalRouter failed: %v", err)
	}
	if got.Name != "vpc-001" {
		t.Fatalf("expected name vpc-001, got %s", got.Name)
	}

	// List
	routers, err := mock.ListLogicalRouters(ctx)
	if err != nil {
		t.Fatalf("ListLogicalRouters failed: %v", err)
	}
	if len(routers) != 1 {
		t.Fatalf("expected 1 router, got %d", len(routers))
	}

	// Delete
	if err := mock.DeleteLogicalRouter(ctx, "vpc-001"); err != nil {
		t.Fatalf("DeleteLogicalRouter failed: %v", err)
	}

	if _, err := mock.GetLogicalRouter(ctx, "vpc-001"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMockOVNClient_LogicalRouterPort_CRUD(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Create router first
	lr := &nbdb.LogicalRouter{Name: "vpc-001"}
	if err := mock.CreateLogicalRouter(ctx, lr); err != nil {
		t.Fatalf("CreateLogicalRouter failed: %v", err)
	}

	// Create router port
	lrp := &nbdb.LogicalRouterPort{
		Name:     "router-to-subnet-a",
		MAC:      "c0:ff:ee:00:00:01",
		Networks: []string{"10.0.1.1/24"},
	}
	if err := mock.CreateLogicalRouterPort(ctx, "vpc-001", lrp); err != nil {
		t.Fatalf("CreateLogicalRouterPort failed: %v", err)
	}

	// Router should have port
	router, _ := mock.GetLogicalRouter(ctx, "vpc-001")
	if len(router.Ports) != 1 {
		t.Fatalf("expected 1 port in router, got %d", len(router.Ports))
	}

	// Create on non-existent router should fail
	if err := mock.CreateLogicalRouterPort(ctx, "no-such-router", &nbdb.LogicalRouterPort{Name: "port-x"}); err == nil {
		t.Fatal("expected error on non-existent router")
	}

	// Delete router port
	if err := mock.DeleteLogicalRouterPort(ctx, "vpc-001", "router-to-subnet-a"); err != nil {
		t.Fatalf("DeleteLogicalRouterPort failed: %v", err)
	}

	router, _ = mock.GetLogicalRouter(ctx, "vpc-001")
	if len(router.Ports) != 0 {
		t.Fatalf("expected 0 ports in router after delete, got %d", len(router.Ports))
	}
}

func TestMockOVNClient_DHCPOptions_CRUD(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Create
	opts := &nbdb.DHCPOptions{
		CIDR: "10.0.1.0/24",
		Options: map[string]string{
			"server_id":  "10.0.1.1",
			"server_mac": "c0:ff:ee:00:00:01",
			"lease_time": "3600",
			"router":     "10.0.1.1",
			"dns_server": "{10.0.1.2}",
			"mtu":        "1442",
		},
		ExternalIDs: map[string]string{"subnet-id": "subnet-001"},
	}
	uuid, err := mock.CreateDHCPOptions(ctx, opts)
	if err != nil {
		t.Fatalf("CreateDHCPOptions failed: %v", err)
	}
	if uuid == "" {
		t.Fatal("expected non-empty UUID")
	}

	// Find by CIDR
	found, err := mock.FindDHCPOptionsByCIDR(ctx, "10.0.1.0/24")
	if err != nil {
		t.Fatalf("FindDHCPOptionsByCIDR failed: %v", err)
	}
	if found.CIDR != "10.0.1.0/24" {
		t.Fatalf("expected CIDR 10.0.1.0/24, got %s", found.CIDR)
	}
	if found.Options["mtu"] != "1442" {
		t.Fatal("options not preserved")
	}

	// Find non-existent should fail
	if _, err := mock.FindDHCPOptionsByCIDR(ctx, "192.168.0.0/16"); err == nil {
		t.Fatal("expected error for non-existent CIDR")
	}

	// List
	all, err := mock.ListDHCPOptions(ctx)
	if err != nil {
		t.Fatalf("ListDHCPOptions failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 DHCP options, got %d", len(all))
	}

	// Delete
	if err := mock.DeleteDHCPOptions(ctx, uuid); err != nil {
		t.Fatalf("DeleteDHCPOptions failed: %v", err)
	}

	if _, err := mock.FindDHCPOptionsByCIDR(ctx, "10.0.1.0/24"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestMockOVNClient_FullVPCTopology(t *testing.T) {
	ctx := context.Background()
	mock := NewMockOVNClient()

	// Simulate creating a full VPC topology:
	// 1. Create logical router (VPC)
	lr := &nbdb.LogicalRouter{
		Name:        "vpc-abc123",
		ExternalIDs: map[string]string{"vpc-id": "vpc-abc123"},
	}
	if err := mock.CreateLogicalRouter(ctx, lr); err != nil {
		t.Fatalf("create router: %v", err)
	}

	// 2. Create logical switch (subnet)
	ls := &nbdb.LogicalSwitch{
		Name:        "subnet-def456",
		ExternalIDs: map[string]string{"subnet-id": "subnet-def456", "vpc-id": "vpc-abc123"},
	}
	if err := mock.CreateLogicalSwitch(ctx, ls); err != nil {
		t.Fatalf("create switch: %v", err)
	}

	// 3. Create DHCP options for the subnet
	dhcp := &nbdb.DHCPOptions{
		CIDR: "10.0.1.0/24",
		Options: map[string]string{
			"server_id":  "10.0.1.1",
			"server_mac": "c0:ff:ee:00:00:01",
			"lease_time": "3600",
			"router":     "10.0.1.1",
			"dns_server": "{10.0.1.2}",
			"mtu":        "1442",
		},
		ExternalIDs: map[string]string{"subnet-id": "subnet-def456"},
	}
	dhcpUUID, err := mock.CreateDHCPOptions(ctx, dhcp)
	if err != nil {
		t.Fatalf("create DHCP options: %v", err)
	}

	// 4. Create router port connecting router to switch
	lrp := &nbdb.LogicalRouterPort{
		Name:     "router-to-subnet-def456",
		MAC:      "c0:ff:ee:00:00:01",
		Networks: []string{"10.0.1.1/24"},
	}
	if err := mock.CreateLogicalRouterPort(ctx, "vpc-abc123", lrp); err != nil {
		t.Fatalf("create router port: %v", err)
	}

	// 5. Create switch port (type=router) connecting switch to router
	routerPort := &nbdb.LogicalSwitchPort{
		Name:    "subnet-def456-to-router",
		Type:    "router",
		Options: map[string]string{"router-port": "router-to-subnet-def456"},
	}
	if err := mock.CreateLogicalSwitchPort(ctx, "subnet-def456", routerPort); err != nil {
		t.Fatalf("create switch router port: %v", err)
	}

	// 6. Create VM port on the switch
	vmPort := &nbdb.LogicalSwitchPort{
		Name:          "port-eni-ghi789",
		Addresses:     []string{"00:00:00:00:01:01 10.0.1.11"},
		PortSecurity:  []string{"00:00:00:00:01:01 10.0.1.11"},
		DHCPv4Options: &dhcpUUID,
		ExternalIDs:   map[string]string{"eni-id": "eni-ghi789"},
	}
	if err := mock.CreateLogicalSwitchPort(ctx, "subnet-def456", vmPort); err != nil {
		t.Fatalf("create VM port: %v", err)
	}

	// Verify topology
	sw, _ := mock.GetLogicalSwitch(ctx, "subnet-def456")
	if len(sw.Ports) != 2 {
		t.Fatalf("expected 2 ports in switch, got %d", len(sw.Ports))
	}

	router, _ := mock.GetLogicalRouter(ctx, "vpc-abc123")
	if len(router.Ports) != 1 {
		t.Fatalf("expected 1 port in router, got %d", len(router.Ports))
	}

	// Verify VM port has DHCP options
	vm, _ := mock.GetLogicalSwitchPort(ctx, "port-eni-ghi789")
	if vm.DHCPv4Options == nil || *vm.DHCPv4Options != dhcpUUID {
		t.Fatal("VM port DHCP options not set correctly")
	}

	// Teardown: delete in reverse order
	if err := mock.DeleteLogicalSwitchPort(ctx, "subnet-def456", "port-eni-ghi789"); err != nil {
		t.Fatalf("delete VM port: %v", err)
	}
	if err := mock.DeleteLogicalSwitchPort(ctx, "subnet-def456", "subnet-def456-to-router"); err != nil {
		t.Fatalf("delete router switch port: %v", err)
	}
	if err := mock.DeleteLogicalRouterPort(ctx, "vpc-abc123", "router-to-subnet-def456"); err != nil {
		t.Fatalf("delete router port: %v", err)
	}
	if err := mock.DeleteDHCPOptions(ctx, dhcpUUID); err != nil {
		t.Fatalf("delete DHCP options: %v", err)
	}
	if err := mock.DeleteLogicalSwitch(ctx, "subnet-def456"); err != nil {
		t.Fatalf("delete switch: %v", err)
	}
	if err := mock.DeleteLogicalRouter(ctx, "vpc-abc123"); err != nil {
		t.Fatalf("delete router: %v", err)
	}

	// Verify empty
	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 0 {
		t.Fatal("expected 0 switches after teardown")
	}
	routers, _ := mock.ListLogicalRouters(ctx)
	if len(routers) != 0 {
		t.Fatal("expected 0 routers after teardown")
	}
}

func TestMockOVNClient_InterfaceCompliance(t *testing.T) {
	// Verify MockOVNClient implements OVNClient
	var _ OVNClient = (*MockOVNClient)(nil)
	// Verify LiveOVNClient implements OVNClient
	var _ OVNClient = (*LiveOVNClient)(nil)
}

func TestNBDB_FullDatabaseModel(t *testing.T) {
	dbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		t.Fatalf("FullDatabaseModel failed: %v", err)
	}
	if dbModel.Name() != "OVN_Northbound" {
		t.Fatalf("expected database name OVN_Northbound, got %s", dbModel.Name())
	}
	types := dbModel.Types()
	expectedTables := []string{
		"Logical_Switch",
		"Logical_Switch_Port",
		"Logical_Router",
		"Logical_Router_Port",
		"DHCP_Options",
	}
	for _, table := range expectedTables {
		if _, ok := types[table]; !ok {
			t.Fatalf("expected table %s in database model", table)
		}
	}
}
