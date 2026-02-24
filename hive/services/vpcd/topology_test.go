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

func TestTopologyHandler_IGWAttach(t *testing.T) {
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

	// Pre-create VPC router
	_ = mock.CreateLogicalRouter(ctx, &nbdb.LogicalRouter{
		Name: "vpc-vpc-igw1",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-igw1",
			"hive:cidr":   "10.0.0.0/16",
		},
	})

	// Attach IGW
	evt := IGWEvent{InternetGatewayId: "igw-test1", VpcId: "vpc-igw1"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWAttach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.igw-attach: %v", err)
	}
	assertSuccess(t, resp, "attach IGW")

	// Verify external switch created
	extSwitch, err := mock.GetLogicalSwitch(ctx, "ext-vpc-igw1")
	if err != nil {
		t.Fatalf("expected external switch: %v", err)
	}
	if extSwitch.ExternalIDs["hive:role"] != "external" {
		t.Errorf("expected role=external, got %s", extSwitch.ExternalIDs["hive:role"])
	}
	if extSwitch.ExternalIDs["hive:igw_id"] != "igw-test1" {
		t.Errorf("expected igw_id=igw-test1, got %s", extSwitch.ExternalIDs["hive:igw_id"])
	}

	// Verify localnet port created on external switch
	_, err = mock.GetLogicalSwitchPort(ctx, "ext-port-vpc-igw1")
	if err != nil {
		t.Fatalf("expected localnet port: %v", err)
	}

	// Verify gateway router port created
	router, err := mock.GetLogicalRouter(ctx, "vpc-vpc-igw1")
	if err != nil {
		t.Fatalf("expected router: %v", err)
	}
	if len(router.Ports) < 1 {
		t.Error("expected at least 1 router port (gateway)")
	}

	// Verify switch gateway port created
	_, err = mock.GetLogicalSwitchPort(ctx, "gw-port-vpc-igw1")
	if err != nil {
		t.Fatalf("expected switch gateway port: %v", err)
	}

	// Verify SNAT rule added
	if len(router.NAT) != 1 {
		t.Errorf("expected 1 NAT rule, got %d", len(router.NAT))
	}

	// Verify default route added
	if len(router.StaticRoutes) != 1 {
		t.Errorf("expected 1 static route, got %d", len(router.StaticRoutes))
	}
}

func TestTopologyHandler_IGWDetach(t *testing.T) {
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

	// Pre-create VPC router
	_ = mock.CreateLogicalRouter(ctx, &nbdb.LogicalRouter{
		Name: "vpc-vpc-igw2",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-igw2",
			"hive:cidr":   "10.0.0.0/16",
		},
	})

	// Attach IGW first
	attachEvt := IGWEvent{InternetGatewayId: "igw-test2", VpcId: "vpc-igw2"}
	data, _ := json.Marshal(attachEvt)
	resp, err := nc.Request(TopicIGWAttach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.igw-attach: %v", err)
	}
	assertSuccess(t, resp, "attach IGW")

	// Verify resources exist before detach
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-igw2")
	if err != nil {
		t.Fatal("expected external switch before detach")
	}

	// Detach IGW
	detachEvt := IGWEvent{InternetGatewayId: "igw-test2", VpcId: "vpc-igw2"}
	data, _ = json.Marshal(detachEvt)
	resp, err = nc.Request(TopicIGWDetach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request vpc.igw-detach: %v", err)
	}
	assertSuccess(t, resp, "detach IGW")

	// Verify external switch deleted
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-igw2")
	if err == nil {
		t.Error("expected external switch to be deleted")
	}

	// Verify router still exists but NAT and routes are cleaned up
	router, err := mock.GetLogicalRouter(ctx, "vpc-vpc-igw2")
	if err != nil {
		t.Fatal("expected VPC router to still exist")
	}
	if len(router.NAT) != 0 {
		t.Errorf("expected 0 NAT rules after detach, got %d", len(router.NAT))
	}
	if len(router.StaticRoutes) != 0 {
		t.Errorf("expected 0 static routes after detach, got %d", len(router.StaticRoutes))
	}
	if len(router.Ports) != 0 {
		t.Errorf("expected 0 router ports after detach, got %d", len(router.Ports))
	}
}

func TestTopologyHandler_IGWAttachDetachLifecycle(t *testing.T) {
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
	vpcEvt := VPCEvent{VpcId: "vpc-igwlc", CidrBlock: "10.0.0.0/16", VNI: 400}
	data, _ := json.Marshal(vpcEvt)
	resp, _ := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create VPC")

	// 2. Create subnet
	subEvt := SubnetEvent{SubnetId: "subnet-igwlc", VpcId: "vpc-igwlc", CidrBlock: "10.0.1.0/24"}
	data, _ = json.Marshal(subEvt)
	resp, _ = nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	assertSuccess(t, resp, "create subnet")

	// 3. Attach IGW
	igwEvt := IGWEvent{InternetGatewayId: "igw-lc1", VpcId: "vpc-igwlc"}
	data, _ = json.Marshal(igwEvt)
	resp, _ = nc.Request(TopicIGWAttach, data, 5_000_000_000)
	assertSuccess(t, resp, "attach IGW")

	// Verify full topology: 2 switches (subnet + external), 1 router with ports+NAT+routes
	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 2 {
		t.Errorf("expected 2 switches (subnet + external), got %d", len(switches))
	}

	router, _ := mock.GetLogicalRouter(ctx, "vpc-vpc-igwlc")
	if len(router.Ports) != 2 {
		t.Errorf("expected 2 router ports (subnet + gateway), got %d", len(router.Ports))
	}

	// 4. Detach IGW
	data, _ = json.Marshal(igwEvt)
	resp, _ = nc.Request(TopicIGWDetach, data, 5_000_000_000)
	assertSuccess(t, resp, "detach IGW")

	// Only subnet switch should remain
	switches, _ = mock.ListLogicalSwitches(ctx)
	if len(switches) != 1 {
		t.Errorf("expected 1 switch after IGW detach, got %d", len(switches))
	}

	// Router should still have subnet port but no gateway port
	router, _ = mock.GetLogicalRouter(ctx, "vpc-vpc-igwlc")
	if len(router.Ports) != 1 {
		t.Errorf("expected 1 router port after IGW detach, got %d", len(router.Ports))
	}

	// 5. Delete subnet and VPC
	data, _ = json.Marshal(subEvt)
	resp, _ = nc.Request(TopicSubnetDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "delete subnet")

	data, _ = json.Marshal(VPCEvent{VpcId: "vpc-igwlc"})
	resp, _ = nc.Request(TopicVPCDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "delete VPC")

	// Everything should be gone
	routers, _ := mock.ListLogicalRouters(ctx)
	if len(routers) != 0 {
		t.Errorf("expected 0 routers, got %d", len(routers))
	}
	switches, _ = mock.ListLogicalSwitches(ctx)
	if len(switches) != 0 {
		t.Errorf("expected 0 switches, got %d", len(switches))
	}
}

func TestTopologyHandler_IGWNilOVN(t *testing.T) {
	_, nc := startTestNATS(t)

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
	evt := IGWEvent{InternetGatewayId: "igw-nil", VpcId: "vpc-nil"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWAttach, data, 5_000_000_000)
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

	// Detach should also fail gracefully
	resp, err = nc.Request(TopicIGWDetach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = json.Unmarshal(resp.Data, &result)
	if result.Success {
		t.Error("expected detach failure when OVN is nil")
	}
}

// --- Error path tests (Phase 3) ---

func assertFailure(t *testing.T, msg *nats.Msg, label string) {
	t.Helper()
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(msg.Data, &result); err != nil {
		t.Fatalf("%s: unmarshal: %v", label, err)
	}
	if result.Success {
		t.Fatalf("%s: expected failure, got success", label)
	}
}

func TestTopologyHandler_BadJSON(t *testing.T) {
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

	badData := []byte("{invalid json")

	// Each topic should return an error response on bad JSON
	topics := []string{
		TopicVPCCreate, TopicVPCDelete,
		TopicSubnetCreate, TopicSubnetDelete,
		TopicCreatePort, TopicDeletePort,
		TopicIGWAttach, TopicIGWDetach,
	}
	for _, topic := range topics {
		resp, err := nc.Request(topic, badData, 5_000_000_000)
		if err != nil {
			t.Fatalf("request %s: %v", topic, err)
		}
		assertFailure(t, resp, "bad JSON on "+topic)
	}
}

func TestTopologyHandler_VPCCreate_DuplicateRouter(t *testing.T) {
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

	// Pre-create router to trigger idempotent path
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-dup", "vpc-dup"))

	evt := VPCEvent{VpcId: "vpc-dup", CidrBlock: "10.0.0.0/16", VNI: 100}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicVPCCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertSuccess(t, resp, "idempotent VPC create")

	// Verify exactly 1 router (no duplicate created)
	routers, _ := mock.ListLogicalRouters(ctx)
	if len(routers) != 1 {
		t.Errorf("expected 1 router (idempotent), got %d", len(routers))
	}
}

func TestTopologyHandler_VPCDelete_RouterNotFound(t *testing.T) {
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

	// Delete VPC that doesn't exist — DeleteLogicalRouter will fail
	evt := VPCEvent{VpcId: "vpc-nonexistent"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicVPCDelete, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "delete nonexistent VPC")
}

func TestTopologyHandler_SubnetCreate_SwitchAlreadyExists(t *testing.T) {
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

	// Pre-create switch to trigger idempotent path
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-exists", "subnet-exists", "vpc-exists"))

	evt := SubnetEvent{SubnetId: "subnet-exists", VpcId: "vpc-exists", CidrBlock: "10.0.1.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertSuccess(t, resp, "idempotent subnet create")

	// Verify exactly 1 switch (no duplicate)
	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 1 {
		t.Errorf("expected 1 switch (idempotent), got %d", len(switches))
	}
}

func TestTopologyHandler_SubnetCreate_RouterPortFails(t *testing.T) {
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

	// Don't create the VPC router — CreateLogicalRouterPort will fail
	evt := SubnetEvent{SubnetId: "subnet-norouter", VpcId: "vpc-norouter", CidrBlock: "10.0.1.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "subnet create without router")

	// Verify switch was cleaned up (best-effort cleanup path)
	_, err = mock.GetLogicalSwitch(ctx, "subnet-subnet-norouter")
	if err == nil {
		t.Error("expected switch to be cleaned up after router port failure")
	}
}

func TestTopologyHandler_SubnetCreate_InvalidCIDR(t *testing.T) {
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

	evt := SubnetEvent{SubnetId: "subnet-badcidr", VpcId: "vpc-badcidr", CidrBlock: "not-a-cidr"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetCreate, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "subnet create with invalid CIDR")
}

func TestTopologyHandler_SubnetDelete_SwitchNotFound(t *testing.T) {
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

	// Delete subnet where nothing exists — switch delete fails at step 4
	evt := SubnetEvent{SubnetId: "subnet-ghost", VpcId: "vpc-ghost", CidrBlock: "10.0.99.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetDelete, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "delete nonexistent subnet")
}

func TestTopologyHandler_CreatePort_SwitchNotFound(t *testing.T) {
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

	// Create port on non-existent switch
	evt := PortEvent{
		NetworkInterfaceId: "eni-orphan",
		SubnetId:           "subnet-missing",
		VpcId:              "vpc-missing",
		PrivateIpAddress:   "10.0.1.4",
		MacAddress:         "02:00:00:00:00:01",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicCreatePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "create port on missing switch")
}

func TestTopologyHandler_CreatePort_Idempotent(t *testing.T) {
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

	// Setup: create switch and port directly
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-subnet-idem", "subnet-idem", "vpc-idem"))
	_ = mock.CreateLogicalSwitchPort(ctx, "subnet-subnet-idem", &nbdb.LogicalSwitchPort{
		Name:      "port-eni-idem",
		Addresses: []string{"02:00:00:11:22:33 10.0.1.4"},
		ExternalIDs: map[string]string{
			"hive:eni_id": "eni-idem",
		},
	})

	// Send create for same port — should succeed (idempotent skip)
	evt := PortEvent{
		NetworkInterfaceId: "eni-idem",
		SubnetId:           "subnet-idem",
		VpcId:              "vpc-idem",
		PrivateIpAddress:   "10.0.1.4",
		MacAddress:         "02:00:00:11:22:33",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicCreatePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertSuccess(t, resp, "idempotent port create")
}

func TestTopologyHandler_DeletePort_PortNotFound(t *testing.T) {
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

	// Delete port that doesn't exist
	evt := PortEvent{
		NetworkInterfaceId: "eni-ghost",
		SubnetId:           "subnet-ghost",
		VpcId:              "vpc-ghost",
		PrivateIpAddress:   "10.0.1.99",
		MacAddress:         "02:00:00:ff:ff:ff",
	}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicDeletePort, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "delete nonexistent port")
}

func TestTopologyHandler_IGWAttach_RouterNotFound(t *testing.T) {
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

	// Attach IGW without VPC router — CreateLogicalRouterPort fails
	evt := IGWEvent{InternetGatewayId: "igw-orphan", VpcId: "vpc-norouter"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWAttach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "IGW attach without router")

	// Verify external switch was cleaned up
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-norouter")
	if err == nil {
		t.Error("expected external switch to be cleaned up after router port failure")
	}
}

func TestTopologyHandler_IGWAttach_Idempotent(t *testing.T) {
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

	// Pre-create external switch to trigger idempotent path
	_ = mock.CreateLogicalSwitch(ctx, &nbdb.LogicalSwitch{
		Name: "ext-vpc-igw-idem",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-igw-idem",
			"hive:role":   "external",
		},
	})

	evt := IGWEvent{InternetGatewayId: "igw-idem", VpcId: "vpc-igw-idem"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWAttach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertSuccess(t, resp, "idempotent IGW attach")
}

func TestTopologyHandler_IGWDetach_PartialCleanup(t *testing.T) {
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

	// Only create the external switch (not the full IGW topology).
	// All intermediate deletes will warn but switch delete should succeed.
	_ = mock.CreateLogicalSwitch(ctx, &nbdb.LogicalSwitch{
		Name: "ext-vpc-partial",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-partial",
			"hive:role":   "external",
		},
	})
	// Create router so NAT cleanup path is exercised (but NAT won't be found)
	_ = mock.CreateLogicalRouter(ctx, &nbdb.LogicalRouter{
		Name: "vpc-vpc-partial",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-partial",
			"hive:cidr":   "10.0.0.0/16",
		},
	})

	evt := IGWEvent{InternetGatewayId: "igw-partial", VpcId: "vpc-partial"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWDetach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Should succeed — intermediate deletes warn but final switch delete succeeds
	assertSuccess(t, resp, "partial IGW detach")

	// Verify external switch was deleted
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-partial")
	if err == nil {
		t.Error("expected external switch to be deleted")
	}
}

func TestTopologyHandler_IGWDetach_NoRouter(t *testing.T) {
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

	// Create external switch but no router — exercises the GetLogicalRouter warn path
	_ = mock.CreateLogicalSwitch(ctx, &nbdb.LogicalSwitch{
		Name: "ext-vpc-nortr",
		ExternalIDs: map[string]string{
			"hive:vpc_id": "vpc-nortr",
			"hive:role":   "external",
		},
	})

	evt := IGWEvent{InternetGatewayId: "igw-nortr", VpcId: "vpc-nortr"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWDetach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Should succeed — router not found is a warn path, switch delete succeeds
	assertSuccess(t, resp, "IGW detach without router")

	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-nortr")
	if err == nil {
		t.Error("expected external switch to be deleted")
	}
}

func TestTopologyHandler_IGWDetach_SwitchNotFound(t *testing.T) {
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

	// No external switch — final DeleteLogicalSwitch fails
	evt := IGWEvent{InternetGatewayId: "igw-nosw", VpcId: "vpc-nosw"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicIGWDetach, data, 5_000_000_000)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	assertFailure(t, resp, "IGW detach without external switch")
}

func TestTopologyHandler_VPCDeleteCascade_WithPorts(t *testing.T) {
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

	// Create VPC router + subnet switch with ports (simulating pre-existing state)
	_ = mock.CreateLogicalRouter(ctx, nbdbLogicalRouter("vpc-vpc-casp", "vpc-casp"))
	_ = mock.CreateLogicalSwitch(ctx, nbdbLogicalSwitch("subnet-sub-casp", "sub-casp", "vpc-casp"))
	_ = mock.CreateLogicalSwitchPort(ctx, "subnet-sub-casp", &nbdb.LogicalSwitchPort{
		Name:      "port-eni-casp1",
		Addresses: []string{"02:00:00:11:22:33 10.0.1.4"},
		ExternalIDs: map[string]string{
			"hive:eni_id":    "eni-casp1",
			"hive:subnet_id": "sub-casp",
			"hive:vpc_id":    "vpc-casp",
		},
	})
	_ = mock.CreateLogicalSwitchPort(ctx, "subnet-sub-casp", &nbdb.LogicalSwitchPort{
		Name:      "port-eni-casp2",
		Addresses: []string{"02:00:00:44:55:66 10.0.1.5"},
		ExternalIDs: map[string]string{
			"hive:eni_id":    "eni-casp2",
			"hive:subnet_id": "sub-casp",
			"hive:vpc_id":    "vpc-casp",
		},
	})
	_, _ = mock.CreateDHCPOptions(ctx, nbdbDHCPOptions("10.0.1.0/24", "sub-casp", "vpc-casp"))

	// Verify switch has 2 ports before delete
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-sub-casp")
	if err != nil {
		t.Fatalf("expected switch: %v", err)
	}
	if len(ls.Ports) != 2 {
		t.Errorf("expected 2 ports before cascade, got %d", len(ls.Ports))
	}

	// Delete VPC should cascade-delete switches (with ports) and DHCP
	evt := VPCEvent{VpcId: "vpc-casp"}
	data, _ := json.Marshal(evt)
	resp, _ := nc.Request(TopicVPCDelete, data, 5_000_000_000)
	assertSuccess(t, resp, "cascade delete VPC with ports")

	// Verify switch is gone
	_, err = mock.GetLogicalSwitch(ctx, "subnet-sub-casp")
	if err == nil {
		t.Error("expected switch to be deleted after cascade")
	}
	// Verify DHCP options gone
	dhcpList, _ := mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 0 {
		t.Errorf("expected 0 DHCP options after cascade, got %d", len(dhcpList))
	}
	// Verify router gone
	_, err = mock.GetLogicalRouter(ctx, "vpc-vpc-casp")
	if err == nil {
		t.Error("expected router to be deleted after cascade")
	}
}

func TestTopologyHandler_SubnetDelete_NilOVN(t *testing.T) {
	_, nc := startTestNATS(t)

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

	evt := SubnetEvent{SubnetId: "subnet-nil", VpcId: "vpc-nil", CidrBlock: "10.0.0.0/24"}
	data, _ := json.Marshal(evt)
	resp, err := nc.Request(TopicSubnetDelete, data, 5_000_000_000)
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
