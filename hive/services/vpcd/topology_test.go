package vpcd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startTestNATS starts an embedded NATS server for testing.
func startTestNATS(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()
	opts := &server.Options{
		Host: "127.0.0.1",
		Port: -1, // auto-assign
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start NATS server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5_000_000_000) { // 5s
		t.Fatal("NATS server not ready")
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		ns.Shutdown()
		t.Fatalf("connect to NATS: %v", err)
	}

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return ns, nc
}

func TestTopologyHandler_VPCCreate(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	evt := VPCEvent{VpcId: "vpc-abc123", CidrBlock: "10.0.0.0/16", VNI: 100}
	data, _ := json.Marshal(evt)

	resp, err := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.create: %v", err)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !result.Success {
		t.Fatalf("vpc.create failed: %s", result.Error)
	}

	// Verify router was created in OVN
	ctx := context.Background()
	lr, err := mock.GetLogicalRouter(ctx, "vpc-vpc-abc123")
	if err != nil {
		t.Fatalf("expected logical router: %v", err)
	}
	if lr.ExternalIDs["hive:vpc_id"] != "vpc-abc123" {
		t.Errorf("expected vpc_id external_id, got %v", lr.ExternalIDs)
	}
	if lr.ExternalIDs["hive:vni"] != "100" {
		t.Errorf("expected vni external_id=100, got %v", lr.ExternalIDs["hive:vni"])
	}
}

func TestTopologyHandler_VPCDelete(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Pre-create a router
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-xyz", "vpc-xyz"))

	// Delete via NATS
	evt := VPCEvent{VpcId: "vpc-xyz"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicVPCDelete, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.delete: %v", err)
	}

	var result struct{ Success bool }
	_ = json.Unmarshal(resp.Data, &result)
	if !result.Success {
		t.Fatal("vpc.delete failed")
	}

	// Verify router is gone
	_, err = mock.GetLogicalRouter(ctx, "vpc-vpc-xyz")
	if err == nil {
		t.Error("expected router to be deleted")
	}
}

func TestTopologyHandler_SubnetCreate(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Pre-create the VPC router
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-sub1", "vpc-sub1"))

	// Create subnet
	evt := SubnetEvent{SubnetId: "subnet-aaa", VpcId: "vpc-sub1", CidrBlock: "10.0.1.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.create-subnet: %v", err)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	_ = json.Unmarshal(resp.Data, &result)
	if !result.Success {
		t.Fatalf("vpc.create-subnet failed: %s", result.Error)
	}

	// Verify logical switch created
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-subnet-aaa")
	if err != nil {
		t.Fatalf("expected logical switch: %v", err)
	}
	if ls.ExternalIDs["hive:subnet_id"] != "subnet-aaa" {
		t.Errorf("expected subnet_id external_id, got %v", ls.ExternalIDs)
	}

	// Verify router port created
	lr, err := mock.GetLogicalRouter(ctx, "vpc-vpc-sub1")
	if err != nil {
		t.Fatalf("expected router: %v", err)
	}
	if len(lr.Ports) != 1 {
		t.Errorf("expected 1 router port, got %d", len(lr.Ports))
	}

	// Verify switch has 1 port (the router port)
	if len(ls.Ports) != 1 {
		t.Errorf("expected 1 switch port, got %d", len(ls.Ports))
	}

	// Verify DHCP options created
	dhcpOpts, err := mock.FindDHCPOptionsByCIDR(ctx, "10.0.1.0/24")
	if err != nil {
		t.Fatalf("expected DHCP options: %v", err)
	}
	if dhcpOpts.Options["router"] != "10.0.1.1" {
		t.Errorf("expected router=10.0.1.1, got %s", dhcpOpts.Options["router"])
	}
	if dhcpOpts.Options["lease_time"] != "3600" {
		t.Errorf("expected lease_time=3600, got %s", dhcpOpts.Options["lease_time"])
	}
	if dhcpOpts.Options["mtu"] != "1442" {
		t.Errorf("expected mtu=1442, got %s", dhcpOpts.Options["mtu"])
	}
}

func TestTopologyHandler_SubnetDelete(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Setup: create VPC router + subnet topology manually
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-del", "vpc-del"))
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-del", "subnet-del", "vpc-del"))
	_ = mock.CreateLogicalRouterPort(ctx, "vpc-vpc-del", nbdbLogicalRouterPort("rtr-subnet-del", "subnet-del", "vpc-del"))
	_ = mock.CreateLogicalSwitchPort(ctx, "subnet-subnet-del", nbdbLogicalSwitchPortRouter("rtr-port-subnet-del", "rtr-subnet-del", "subnet-del", "vpc-del"))
	_, _ = mock.CreateDHCPOptions(ctx, nbdbDHCPOptions("10.0.2.0/24", "subnet-del", "vpc-del"))

	// Delete subnet via NATS
	evt := SubnetEvent{SubnetId: "subnet-del", VpcId: "vpc-del", CidrBlock: "10.0.2.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetDelete, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.delete-subnet: %v", err)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	_ = json.Unmarshal(resp.Data, &result)
	if !result.Success {
		t.Fatalf("vpc.delete-subnet failed: %s", result.Error)
	}

	// Verify switch is deleted
	_, err = mock.GetLogicalSwitch(ctx, "subnet-subnet-del")
	if err == nil {
		t.Error("expected switch to be deleted")
	}

	// Verify DHCP options are deleted
	_, err = mock.FindDHCPOptionsByCIDR(ctx, "10.0.2.0/24")
	if err == nil {
		t.Error("expected DHCP options to be deleted")
	}

	// Verify router still exists (only subnet topology deleted, not VPC)
	_, err = mock.GetLogicalRouter(ctx, "vpc-vpc-del")
	if err != nil {
		t.Error("expected VPC router to still exist")
	}
}

func TestTopologyHandler_FullLifecycle(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// 1. Create VPC
	vpcEvt := VPCEvent{VpcId: "vpc-full", CidrBlock: "10.0.0.0/16", VNI: 200}
	data, _ := json.Marshal(vpcEvt)
	resp, _ := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create VPC")

	// 2. Create Subnet 1
	subEvt1 := SubnetEvent{SubnetId: "subnet-a", VpcId: "vpc-full", CidrBlock: "10.0.1.0/24"}
	data, _ = json.Marshal(subEvt1)
	resp, _ = nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create subnet-a")

	// 3. Create Subnet 2
	subEvt2 := SubnetEvent{SubnetId: "subnet-b", VpcId: "vpc-full", CidrBlock: "10.0.2.0/24"}
	data, _ = json.Marshal(subEvt2)
	resp, _ = nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create subnet-b")

	// Verify: 1 router with 2 ports, 2 switches, 2 DHCP option sets
	routers, _ := mock.ListLogicalRouters(ctx)
	if len(routers) != 1 {
		t.Errorf("expected 1 router, got %d", len(routers))
	}
	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 2 {
		t.Errorf("expected 2 switches, got %d", len(switches))
	}
	dhcpList, _ := mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 2 {
		t.Errorf("expected 2 DHCP option sets, got %d", len(dhcpList))
	}

	// 4. Delete Subnet 1
	data, _ = json.Marshal(subEvt1)
	resp, _ = nc.Request(TopicSubnetDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "delete subnet-a")

	switches, _ = mock.ListLogicalSwitches(ctx)
	if len(switches) != 1 {
		t.Errorf("expected 1 switch after delete, got %d", len(switches))
	}

	// 5. Delete Subnet 2
	data, _ = json.Marshal(subEvt2)
	resp, _ = nc.Request(TopicSubnetDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "delete subnet-b")

	// 6. Delete VPC
	delEvt := VPCEvent{VpcId: "vpc-full"}
	data, _ = json.Marshal(delEvt)
	resp, _ = nc.Request(TopicVPCDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "delete VPC")

	// Verify everything is gone
	routers, _ = mock.ListLogicalRouters(ctx)
	if len(routers) != 0 {
		t.Errorf("expected 0 routers, got %d", len(routers))
	}
	switches, _ = mock.ListLogicalSwitches(ctx)
	if len(switches) != 0 {
		t.Errorf("expected 0 switches, got %d", len(switches))
	}
	dhcpList, _ = mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 0 {
		t.Errorf("expected 0 DHCP options, got %d", len(dhcpList))
	}
}

func TestTopologyHandler_VPCDeleteCascade(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Create VPC + subnet directly in mock (simulating pre-existing state)
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-cas", "vpc-cas"))
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-sub-cas", "sub-cas", "vpc-cas"))
	_, _ = mock.CreateDHCPOptions(ctx, nbdbDHCPOptions("10.0.3.0/24", "sub-cas", "vpc-cas"))

	// Delete VPC should cascade-delete switches and DHCP
	evt := VPCEvent{VpcId: "vpc-cas"}
	data, _ := json.Marshal(evt)
	resp, _ := nc.Request(TopicVPCDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "cascade delete VPC")

	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 0 {
		t.Errorf("expected 0 switches after cascade delete, got %d", len(switches))
	}
	dhcpList, _ := mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 0 {
		t.Errorf("expected 0 DHCP options after cascade delete, got %d", len(dhcpList))
	}
}

func TestSubnetGateway(t *testing.T) {
	tests := []struct {
		cidr     string
		wantIP   string
		wantMask int
		wantErr  bool
	}{
		{"10.0.1.0/24", "10.0.1.1", 24, false},
		{"192.168.0.0/16", "192.168.0.1", 16, false},
		{"172.16.0.0/20", "172.16.0.1", 20, false},
		{"invalid", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			ip, mask, err := subnetGateway(tt.cidr)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ip != tt.wantIP {
				t.Errorf("expected IP %s, got %s", tt.wantIP, ip)
			}
			if mask != tt.wantMask {
				t.Errorf("expected mask %d, got %d", tt.wantMask, mask)
			}
		})
	}
}

func TestGenerateMAC(t *testing.T) {
	mac1 := generateMAC("subnet-aaa")
	mac2 := generateMAC("subnet-bbb")

	// Must start with locally-administered unicast prefix
	if mac1[:8] != "02:00:00" {
		t.Errorf("expected prefix 02:00:00, got %s", mac1[:8])
	}

	// Different inputs produce different MACs
	if mac1 == mac2 {
		t.Error("expected different MACs for different inputs")
	}

	// Same input produces same MAC (deterministic)
	mac1b := generateMAC("subnet-aaa")
	if mac1 != mac1b {
		t.Error("expected deterministic MAC")
	}
}

func TestTopologyHandler_CreatePort(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Setup: create VPC router, subnet switch, and DHCP options
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-port1", "vpc-port1"))
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-port1", "subnet-port1", "vpc-port1"))
	dhcpUUID, _ := mock.CreateDHCPOptions(ctx, nbdbDHCPOptions("10.0.1.0/24", "subnet-port1", "vpc-port1"))

	// Create port via NATS
	evt := PortEvent{
		NetworkInterfaceId: "eni-aaa111",
		SubnetId:           "subnet-port1",
		VpcId:              "vpc-port1",
		PrivateIpAddress:   "10.0.1.4",
		MacAddress:         "02:00:00:11:22:33",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicCreatePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.create-port: %v", err)
	}
	assertSuccess(t, resp, "create port")

	// Verify logical switch port created
	lsp, err := mock.GetLogicalSwitchPort(ctx, "port-eni-aaa111")
	if err != nil {
		t.Fatalf("expected logical switch port: %v", err)
	}

	// Verify addresses
	expectedAddr := "02:00:00:11:22:33 10.0.1.4"
	if len(lsp.Addresses) != 1 || lsp.Addresses[0] != expectedAddr {
		t.Errorf("expected addresses [%s], got %v", expectedAddr, lsp.Addresses)
	}

	// Verify port security
	if len(lsp.PortSecurity) != 1 || lsp.PortSecurity[0] != expectedAddr {
		t.Errorf("expected port_security [%s], got %v", expectedAddr, lsp.PortSecurity)
	}

	// Verify DHCPv4 options
	if lsp.DHCPv4Options == nil {
		t.Fatal("expected DHCPv4Options to be set")
	}
	if *lsp.DHCPv4Options != dhcpUUID {
		t.Errorf("expected DHCPv4Options UUID %s, got %s", dhcpUUID, *lsp.DHCPv4Options)
	}

	// Verify external IDs
	if lsp.ExternalIDs["hive:eni_id"] != "eni-aaa111" {
		t.Errorf("expected eni_id=eni-aaa111, got %s", lsp.ExternalIDs["hive:eni_id"])
	}
	if lsp.ExternalIDs["hive:subnet_id"] != "subnet-port1" {
		t.Errorf("expected subnet_id=subnet-port1, got %s", lsp.ExternalIDs["hive:subnet_id"])
	}
	if lsp.ExternalIDs["hive:vpc_id"] != "vpc-port1" {
		t.Errorf("expected vpc_id=vpc-port1, got %s", lsp.ExternalIDs["hive:vpc_id"])
	}

	// Verify port was added to the switch
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-subnet-port1")
	if err != nil {
		t.Fatalf("get switch: %v", err)
	}
	if len(ls.Ports) != 1 {
		t.Errorf("expected 1 port on switch, got %d", len(ls.Ports))
	}
}

func TestTopologyHandler_DeletePort(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Setup: create switch and port
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-del2", "subnet-del2", "vpc-del2"))
	_ = mock.CreateLogicalSwitchPort(ctx, "subnet-subnet-del2", &nbdb.LogicalSwitchPort{
		Name:         "port-eni-bbb222",
		Addresses:    []string{"02:00:00:44:55:66 10.0.2.4"},
		PortSecurity: []string{"02:00:00:44:55:66 10.0.2.4"},
		ExternalIDs: map[string]string{
			"hive:eni_id":    "eni-bbb222",
			"hive:subnet_id": "subnet-del2",
			"hive:vpc_id":    "vpc-del2",
		},
	})

	// Verify port exists before delete
	_, err = mock.GetLogicalSwitchPort(ctx, "port-eni-bbb222")
	if err != nil {
		t.Fatalf("expected port to exist before delete: %v", err)
	}

	// Delete port via NATS
	evt := PortEvent{
		NetworkInterfaceId: "eni-bbb222",
		SubnetId:           "subnet-del2",
		VpcId:              "vpc-del2",
		PrivateIpAddress:   "10.0.2.4",
		MacAddress:         "02:00:00:44:55:66",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicDeletePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.delete-port: %v", err)
	}
	assertSuccess(t, resp, "delete port")

	// Verify port is gone
	_, err = mock.GetLogicalSwitchPort(ctx, "port-eni-bbb222")
	if err == nil {
		t.Error("expected port to be deleted")
	}

	// Verify switch still exists but has no ports
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-subnet-del2")
	if err != nil {
		t.Fatalf("expected switch to still exist: %v", err)
	}
	if len(ls.Ports) != 0 {
		t.Errorf("expected 0 ports on switch after delete, got %d", len(ls.Ports))
	}
}

func TestTopologyHandler_CreatePortNoDHCP(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Setup: create switch but NO DHCP options
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-nodhcp", "subnet-nodhcp", "vpc-nodhcp"))

	// Create port — should succeed but without DHCPv4Options
	evt := PortEvent{
		NetworkInterfaceId: "eni-nodhcp",
		SubnetId:           "subnet-nodhcp",
		VpcId:              "vpc-nodhcp",
		PrivateIpAddress:   "10.0.3.4",
		MacAddress:         "02:00:00:77:88:99",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicCreatePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.create-port: %v", err)
	}
	assertSuccess(t, resp, "create port without DHCP")

	// Port should exist but without DHCPv4Options
	lsp, err := mock.GetLogicalSwitchPort(ctx, "port-eni-nodhcp")
	if err != nil {
		t.Fatalf("expected port: %v", err)
	}
	if lsp.DHCPv4Options != nil {
		t.Errorf("expected nil DHCPv4Options when no DHCP configured, got %s", *lsp.DHCPv4Options)
	}
}

func TestTopologyHandler_PortLifecycle(t *testing.T) {
	_, nc := startTestNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// 1. Create VPC
	vpcEvt := VPCEvent{VpcId: "vpc-plc", CidrBlock: "10.0.0.0/16", VNI: 300}
	data, _ := json.Marshal(vpcEvt)
	resp, _ := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create VPC")

	// 2. Create Subnet
	subEvt := SubnetEvent{SubnetId: "subnet-plc", VpcId: "vpc-plc", CidrBlock: "10.0.1.0/24"}
	data, _ = json.Marshal(subEvt)
	resp, _ = nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create subnet")

	// 3. Create two ports
	portEvt1 := PortEvent{
		NetworkInterfaceId: "eni-plc-1",
		SubnetId:           "subnet-plc",
		VpcId:              "vpc-plc",
		PrivateIpAddress:   "10.0.1.4",
		MacAddress:         "02:00:00:aa:bb:01",
	}
	data, _ = json.Marshal(portEvt1)
	resp, _ = nc.Request(TopicCreatePort, data, 5_000_000_000)
	assertSuccess(t, resp, "create port 1")

	portEvt2 := PortEvent{
		NetworkInterfaceId: "eni-plc-2",
		SubnetId:           "subnet-plc",
		VpcId:              "vpc-plc",
		PrivateIpAddress:   "10.0.1.5",
		MacAddress:         "02:00:00:aa:bb:02",
	}
	data, _ = json.Marshal(portEvt2)
	resp, _ = nc.Request(TopicCreatePort, data, 5_000_000_000)
	assertSuccess(t, resp, "create port 2")

	// Verify switch has 3 ports (router port + 2 ENI ports)
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-subnet-plc")
	if err != nil {
		t.Fatalf("get switch: %v", err)
	}
	if len(ls.Ports) != 3 {
		t.Errorf("expected 3 ports (1 router + 2 ENI), got %d", len(ls.Ports))
	}

	// 4. Delete port 1
	data, _ = json.Marshal(portEvt1)
	resp, _ = nc.Request(TopicDeletePort, data, 5_000_000_000)
	assertSuccess(t, resp, "delete port 1")

	// Verify switch has 2 ports now
	ls, err = mock.GetLogicalSwitch(ctx, "subnet-subnet-plc")
	if err != nil {
		t.Fatalf("get switch after port delete: %v", err)
	}
	if len(ls.Ports) != 2 {
		t.Errorf("expected 2 ports after delete, got %d", len(ls.Ports))
	}

	// Port 2 should still exist
	_, err = mock.GetLogicalSwitchPort(ctx, "port-eni-plc-2")
	if err != nil {
		t.Error("expected port 2 to still exist")
	}

	// Port 1 should be gone
	_, err = mock.GetLogicalSwitchPort(ctx, "port-eni-plc-1")
	if err == nil {
		t.Error("expected port 1 to be deleted")
	}
}

func TestTopologyHandler_NilOVN(t *testing.T) {
	_, nc := startTestNATS(t)

	// nil OVN client (OVN not connected)
	topo := NewTopologyHandler(nil)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// Should fail gracefully when OVN is nil
	evt := VPCEvent{VpcId: "vpc-nil", CidrBlock: "10.0.0.0/16", VNI: 100}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	_ = json.Unmarshal(resp.Data, &result)
	if result.Success {
		t.Error("expected failure when OVN is nil")
	}

	// Port create should also fail gracefully
	portEvt := PortEvent{
		NetworkInterfaceId: "eni-nil",
		SubnetId:           "subnet-nil",
		VpcId:              "vpc-nil",
		PrivateIpAddress:   "10.0.1.4",
		MacAddress:         "02:00:00:00:00:01",
	}
	data, _ = json.Marshal(portEvt)
	resp, err = nc.Request(TopicCreatePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request create port: %v", err)
	}
	_ = json.Unmarshal(resp.Data, &result)
	if result.Success {
		t.Error("expected create-port failure when OVN is nil")
	}

	// Port delete should also fail gracefully
	resp, err = nc.Request(TopicDeletePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request delete port: %v", err)
	}
	_ = json.Unmarshal(resp.Data, &result)
	if result.Success {
		t.Error("expected delete-port failure when OVN is nil")
	}
}

// --- Test helpers ---

func assertSuccess(t *testing.T, msg *nats.Msg, label string) {
	t.Helper()
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &result); err != nil {
		t.Fatalf("%s: unmarshal: %v", label, err)
	}
	if !result.Success {
		t.Fatalf("%s: failed: %s", label, result.Error)
	}
}

// nbdb helper factories for tests

func nbdbLogicalRouter(name, vpcId string) *nbdb.LogicalRouter {
	return &nbdb.LogicalRouter{
		Name: name,
		ExternalIDs: map[string]string{
			"hive:vpc_id": vpcId,
		},
	}
}

func nbdbLogicalSwitch(name, subnetId, vpcId string) *nbdb.LogicalSwitch {
	return &nbdb.LogicalSwitch{
		Name: name,
		ExternalIDs: map[string]string{
			"hive:subnet_id": subnetId,
			"hive:vpc_id":    vpcId,
		},
	}
}

func nbdbLogicalRouterPort(name, subnetId, vpcId string) *nbdb.LogicalRouterPort {
	return &nbdb.LogicalRouterPort{
		Name:     name,
		MAC:      "02:00:00:aa:bb:cc",
		Networks: []string{"10.0.2.1/24"},
		ExternalIDs: map[string]string{
			"hive:subnet_id": subnetId,
			"hive:vpc_id":    vpcId,
		},
	}
}

func nbdbLogicalSwitchPortRouter(name, routerPort, subnetId, vpcId string) *nbdb.LogicalSwitchPort {
	return &nbdb.LogicalSwitchPort{
		Name:      name,
		Type:      "router",
		Addresses: []string{"router"},
		Options: map[string]string{
			"router-port": routerPort,
		},
		ExternalIDs: map[string]string{
			"hive:subnet_id": subnetId,
			"hive:vpc_id":    vpcId,
		},
	}
}

func nbdbDHCPOptions(cidr, subnetId, vpcId string) *nbdb.DHCPOptions {
	return &nbdb.DHCPOptions{
		CIDR: cidr,
		Options: map[string]string{
			"router":     "10.0.2.1",
			"lease_time": "3600",
		},
		ExternalIDs: map[string]string{
			"hive:subnet_id": subnetId,
			"hive:vpc_id":    vpcId,
		},
	}
}
