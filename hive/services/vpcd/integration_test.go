package vpcd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_igw "github.com/mulgadc/hive/hive/handlers/ec2/igw"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startTestJetStreamNATS starts an embedded NATS server with JetStream for integration tests.
func startTestJetStreamNATS(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start NATS server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5_000_000_000) {
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

// TestIntegration_VPCLifecycle tests the full VPC lifecycle:
// VPC create → subnet create → ENI create → IGW attach → IGW detach → cleanup.
// This validates the NATS event flow between the daemon and vpcd topology handler.
func TestIntegration_VPCLifecycle(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
	mock := NewMockOVNClient()
	_ = mock.Connect(context.Background())
	ctx := context.Background()

	// Set up topology handler subscriptions (simulates vpcd)
	topo := NewTopologyHandler(mock)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		t.Fatalf("subscribe topology handler: %v", err)
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	// === Phase 1: Create VPC ===
	vpcEvt := VPCEvent{VpcId: "vpc-integ1", CidrBlock: "10.0.0.0/16", VNI: 1000}
	vpcData, _ := json.Marshal(vpcEvt)
	resp, err := nc.Request(TopicVPCCreate, vpcData, 5*time.Second)
	if err != nil {
		t.Fatalf("vpc.create: %v", err)
	}
	assertSuccess(t, resp, "create VPC")

	// Verify OVN: logical router exists
	router, err := mock.GetLogicalRouter(ctx, "vpc-vpc-integ1")
	if err != nil {
		t.Fatalf("expected VPC router: %v", err)
	}
	if router.ExternalIDs["hive:vpc_id"] != "vpc-integ1" {
		t.Errorf("router vpc_id = %s, want vpc-integ1", router.ExternalIDs["hive:vpc_id"])
	}

	// === Phase 2: Create Subnet ===
	subEvt := SubnetEvent{SubnetId: "subnet-integ1", VpcId: "vpc-integ1", CidrBlock: "10.0.1.0/24"}
	subData, _ := json.Marshal(subEvt)
	resp, err = nc.Request(TopicSubnetCreate, subData, 5*time.Second)
	if err != nil {
		t.Fatalf("vpc.create-subnet: %v", err)
	}
	assertSuccess(t, resp, "create subnet")

	// Verify OVN: logical switch, router port, DHCP
	ls, err := mock.GetLogicalSwitch(ctx, "subnet-subnet-integ1")
	if err != nil {
		t.Fatalf("expected logical switch: %v", err)
	}
	if ls.ExternalIDs["hive:subnet_id"] != "subnet-integ1" {
		t.Errorf("switch subnet_id = %s, want subnet-integ1", ls.ExternalIDs["hive:subnet_id"])
	}
	dhcp, err := mock.FindDHCPOptionsByCIDR(ctx, "10.0.1.0/24")
	if err != nil {
		t.Fatalf("expected DHCP options: %v", err)
	}
	if dhcp.Options["router"] != "10.0.1.1" {
		t.Errorf("DHCP router = %s, want 10.0.1.1", dhcp.Options["router"])
	}

	// Verify router has 1 port (subnet port)
	router, _ = mock.GetLogicalRouter(ctx, "vpc-vpc-integ1")
	if len(router.Ports) != 1 {
		t.Errorf("router ports = %d, want 1", len(router.Ports))
	}

	// === Phase 3: Create ENI port ===
	portEvt := PortEvent{
		NetworkInterfaceId: "eni-integ1",
		SubnetId:           "subnet-integ1",
		VpcId:              "vpc-integ1",
		PrivateIpAddress:   "10.0.1.10",
		MacAddress:         "02:00:00:aa:bb:01",
	}
	portData, _ := json.Marshal(portEvt)
	resp, err = nc.Request(TopicCreatePort, portData, 5*time.Second)
	if err != nil {
		t.Fatalf("vpc.create-port: %v", err)
	}
	assertSuccess(t, resp, "create ENI port")

	// Verify OVN: logical switch port with correct addresses and DHCP
	lsp, err := mock.GetLogicalSwitchPort(ctx, "port-eni-integ1")
	if err != nil {
		t.Fatalf("expected logical switch port: %v", err)
	}
	if lsp.Addresses[0] != "02:00:00:aa:bb:01 10.0.1.10" {
		t.Errorf("port addresses = %v, want [02:00:00:aa:bb:01 10.0.1.10]", lsp.Addresses)
	}
	if lsp.DHCPv4Options == nil || *lsp.DHCPv4Options != dhcp.UUID {
		t.Error("port should have DHCPv4Options set to subnet DHCP UUID")
	}

	// Verify switch has 2 ports (router port + ENI port)
	ls, _ = mock.GetLogicalSwitch(ctx, "subnet-subnet-integ1")
	if len(ls.Ports) != 2 {
		t.Errorf("switch ports = %d, want 2 (router + ENI)", len(ls.Ports))
	}

	// === Phase 4: Create and Attach Internet Gateway ===
	igwSvc, err := handlers_ec2_igw.NewIGWServiceImplWithNATS(nil, nc)
	if err != nil {
		t.Fatalf("create IGW service: %v", err)
	}

	igwOut, err := igwSvc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("internet-gateway"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("integ-igw")},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateInternetGateway: %v", err)
	}
	igwID := *igwOut.InternetGateway.InternetGatewayId

	// Attach IGW to VPC
	_, err = igwSvc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-integ1"),
	})
	if err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}

	// Wait briefly for NATS event to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify OVN: external switch, SNAT, default route
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-integ1")
	if err != nil {
		t.Fatalf("expected external switch after IGW attach: %v", err)
	}

	router, _ = mock.GetLogicalRouter(ctx, "vpc-vpc-integ1")
	// Router should have 2 ports (subnet + gateway)
	if len(router.Ports) != 2 {
		t.Errorf("router ports = %d, want 2 (subnet + gateway)", len(router.Ports))
	}
	// NAT rule should exist
	if len(router.NAT) != 1 {
		t.Errorf("router NAT rules = %d, want 1", len(router.NAT))
	}
	// Default route should exist
	if len(router.StaticRoutes) != 1 {
		t.Errorf("router static routes = %d, want 1", len(router.StaticRoutes))
	}

	// Verify IGW state via Describe
	descOut, err := igwSvc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{aws.String(igwID)},
	})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}
	if len(descOut.InternetGateways) != 1 {
		t.Fatalf("expected 1 IGW, got %d", len(descOut.InternetGateways))
	}
	if len(descOut.InternetGateways[0].Attachments) != 1 {
		t.Fatal("expected IGW to have 1 attachment")
	}
	if *descOut.InternetGateways[0].Attachments[0].VpcId != "vpc-integ1" {
		t.Errorf("IGW attachment VpcId = %s, want vpc-integ1", *descOut.InternetGateways[0].Attachments[0].VpcId)
	}

	// === Phase 5: Verify Full Topology Summary ===
	switches, _ := mock.ListLogicalSwitches(ctx)
	if len(switches) != 2 {
		t.Errorf("total switches = %d, want 2 (subnet + external)", len(switches))
	}
	routers, _ := mock.ListLogicalRouters(ctx)
	if len(routers) != 1 {
		t.Errorf("total routers = %d, want 1", len(routers))
	}
	dhcpList, _ := mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 1 {
		t.Errorf("total DHCP options = %d, want 1", len(dhcpList))
	}

	// === Phase 6: Detach IGW ===
	_, err = igwSvc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-integ1"),
	})
	if err != nil {
		t.Fatalf("DetachInternetGateway: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// External switch should be cleaned up
	_, err = mock.GetLogicalSwitch(ctx, "ext-vpc-integ1")
	if err == nil {
		t.Error("expected external switch to be deleted after IGW detach")
	}

	// Router should have 1 port (subnet only), no NAT, no routes
	router, _ = mock.GetLogicalRouter(ctx, "vpc-vpc-integ1")
	if len(router.Ports) != 1 {
		t.Errorf("router ports after detach = %d, want 1", len(router.Ports))
	}
	if len(router.NAT) != 0 {
		t.Errorf("router NAT after detach = %d, want 0", len(router.NAT))
	}
	if len(router.StaticRoutes) != 0 {
		t.Errorf("router routes after detach = %d, want 0", len(router.StaticRoutes))
	}

	// === Phase 7: Delete IGW ===
	_, err = igwSvc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	if err != nil {
		t.Fatalf("DeleteInternetGateway: %v", err)
	}

	// === Phase 8: Clean up — delete ENI, subnet, VPC ===
	resp, _ = nc.Request(TopicDeletePort, portData, 5*time.Second)
	assertSuccess(t, resp, "delete ENI port")

	resp, _ = nc.Request(TopicSubnetDelete, subData, 5*time.Second)
	assertSuccess(t, resp, "delete subnet")

	resp, _ = nc.Request(TopicVPCDelete, vpcData, 5*time.Second)
	assertSuccess(t, resp, "delete VPC")

	// === Phase 9: Verify Complete Cleanup ===
	switches, _ = mock.ListLogicalSwitches(ctx)
	if len(switches) != 0 {
		t.Errorf("expected 0 switches after cleanup, got %d", len(switches))
	}
	routers, _ = mock.ListLogicalRouters(ctx)
	if len(routers) != 0 {
		t.Errorf("expected 0 routers after cleanup, got %d", len(routers))
	}
	dhcpList, _ = mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 0 {
		t.Errorf("expected 0 DHCP options after cleanup, got %d", len(dhcpList))
	}

	descOut, err = igwSvc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{})
	if err != nil {
		t.Fatalf("DescribeInternetGateways: %v", err)
	}
	if len(descOut.InternetGateways) != 0 {
		t.Errorf("expected 0 IGWs after cleanup, got %d", len(descOut.InternetGateways))
	}
}

// TestIntegration_MultiSubnetWithIGW tests a VPC with multiple subnets and IGW.
func TestIntegration_MultiSubnetWithIGW(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
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

	// Create VPC
	vpcEvt := VPCEvent{VpcId: "vpc-multi", CidrBlock: "10.0.0.0/16", VNI: 2000}
	vpcData, _ := json.Marshal(vpcEvt)
	resp, _ := nc.Request(TopicVPCCreate, vpcData, 5*time.Second)
	assertSuccess(t, resp, "create VPC")

	// Create 3 subnets in the same VPC
	subnets := []SubnetEvent{
		{SubnetId: "subnet-a", VpcId: "vpc-multi", CidrBlock: "10.0.1.0/24"},
		{SubnetId: "subnet-b", VpcId: "vpc-multi", CidrBlock: "10.0.2.0/24"},
		{SubnetId: "subnet-c", VpcId: "vpc-multi", CidrBlock: "10.0.3.0/24"},
	}
	for _, sub := range subnets {
		data, _ := json.Marshal(sub)
		resp, _ = nc.Request(TopicSubnetCreate, data, 5*time.Second)
		assertSuccess(t, resp, "create subnet "+sub.SubnetId)
	}

	// Create ENI in each subnet
	ports := []PortEvent{
		{NetworkInterfaceId: "eni-a1", SubnetId: "subnet-a", VpcId: "vpc-multi", PrivateIpAddress: "10.0.1.10", MacAddress: "02:00:00:01:01:01"},
		{NetworkInterfaceId: "eni-b1", SubnetId: "subnet-b", VpcId: "vpc-multi", PrivateIpAddress: "10.0.2.10", MacAddress: "02:00:00:02:02:02"},
		{NetworkInterfaceId: "eni-c1", SubnetId: "subnet-c", VpcId: "vpc-multi", PrivateIpAddress: "10.0.3.10", MacAddress: "02:00:00:03:03:03"},
	}
	for _, port := range ports {
		data, _ := json.Marshal(port)
		resp, _ = nc.Request(TopicCreatePort, data, 5*time.Second)
		assertSuccess(t, resp, "create port "+port.NetworkInterfaceId)
	}

	// Verify: 1 router, 3 switches, 3 DHCP options
	routerList, _ := mock.ListLogicalRouters(ctx)
	if len(routerList) != 1 {
		t.Errorf("routers = %d, want 1", len(routerList))
	}
	switchList, _ := mock.ListLogicalSwitches(ctx)
	if len(switchList) != 3 {
		t.Errorf("switches = %d, want 3", len(switchList))
	}
	dhcpList, _ := mock.ListDHCPOptions(ctx)
	if len(dhcpList) != 3 {
		t.Errorf("DHCP options = %d, want 3", len(dhcpList))
	}

	// Router should have 3 ports (one per subnet)
	router, _ := mock.GetLogicalRouter(ctx, "vpc-vpc-multi")
	if len(router.Ports) != 3 {
		t.Errorf("router ports = %d, want 3", len(router.Ports))
	}

	// Attach IGW
	igwSvc, _ := handlers_ec2_igw.NewIGWServiceImplWithNATS(nil, nc)
	igwOut, _ := igwSvc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{})
	igwID := *igwOut.InternetGateway.InternetGatewayId

	_, err = igwSvc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-multi"),
	})
	if err != nil {
		t.Fatalf("AttachInternetGateway: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Now: 4 switches (3 subnets + 1 external), router has 4 ports + NAT + route
	switchList, _ = mock.ListLogicalSwitches(ctx)
	if len(switchList) != 4 {
		t.Errorf("switches after IGW = %d, want 4", len(switchList))
	}
	router, _ = mock.GetLogicalRouter(ctx, "vpc-vpc-multi")
	if len(router.Ports) != 4 {
		t.Errorf("router ports after IGW = %d, want 4", len(router.Ports))
	}
	if len(router.NAT) != 1 {
		t.Errorf("router NAT = %d, want 1", len(router.NAT))
	}
	if len(router.StaticRoutes) != 1 {
		t.Errorf("router routes = %d, want 1", len(router.StaticRoutes))
	}

	// === Cleanup: detach IGW, delete everything ===
	_, _ = igwSvc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-multi"),
	})
	time.Sleep(100 * time.Millisecond)
	_, _ = igwSvc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})

	// Delete ports
	for _, port := range ports {
		data, _ := json.Marshal(port)
		resp, _ = nc.Request(TopicDeletePort, data, 5*time.Second)
		assertSuccess(t, resp, "delete port "+port.NetworkInterfaceId)
	}

	// Delete subnets
	for _, sub := range subnets {
		data, _ := json.Marshal(sub)
		resp, _ = nc.Request(TopicSubnetDelete, data, 5*time.Second)
		assertSuccess(t, resp, "delete subnet "+sub.SubnetId)
	}

	// Delete VPC
	resp, _ = nc.Request(TopicVPCDelete, vpcData, 5*time.Second)
	assertSuccess(t, resp, "delete VPC")

	// Verify complete cleanup
	routerList, _ = mock.ListLogicalRouters(ctx)
	if len(routerList) != 0 {
		t.Errorf("routers after cleanup = %d, want 0", len(routerList))
	}
	switchList, _ = mock.ListLogicalSwitches(ctx)
	if len(switchList) != 0 {
		t.Errorf("switches after cleanup = %d, want 0", len(switchList))
	}
}

// TestIntegration_IGWErrorPaths tests IGW error handling across the integration boundary.
func TestIntegration_IGWErrorPaths(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
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

	igwSvc, err := handlers_ec2_igw.NewIGWServiceImplWithNATS(nil, nc)
	if err != nil {
		t.Fatalf("create IGW service: %v", err)
	}

	// Create and attach IGW
	igwOut, _ := igwSvc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{})
	igwID := *igwOut.InternetGateway.InternetGatewayId

	// Can't delete while attached
	_, _ = igwSvc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-err1"),
	})

	_, err = igwSvc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	if err == nil {
		t.Error("expected DependencyViolation error")
	}

	// Can't attach twice
	_, err = igwSvc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-err2"),
	})
	if err == nil {
		t.Error("expected ResourceAlreadyAssociated error")
	}

	// Can't detach from wrong VPC
	_, err = igwSvc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-wrong"),
	})
	if err == nil {
		t.Error("expected GatewayNotAttached error")
	}

	// Operations on nonexistent IGW
	_, err = igwSvc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-doesnotexist"),
		VpcId:             aws.String("vpc-err1"),
	})
	if err == nil {
		t.Error("expected NotFound error for attach")
	}

	_, err = igwSvc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-doesnotexist"),
		VpcId:             aws.String("vpc-err1"),
	})
	if err == nil {
		t.Error("expected NotFound error for detach")
	}

	// Cleanup
	_, _ = igwSvc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-err1"),
	})
	_, _ = igwSvc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
}
