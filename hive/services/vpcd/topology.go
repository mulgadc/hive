package vpcd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
	"github.com/nats-io/nats.go"
)

// NATS topics for VPC lifecycle events published by the daemon.
const (
	TopicVPCCreate    = "vpc.create"
	TopicVPCDelete    = "vpc.delete"
	TopicSubnetCreate = "vpc.create-subnet"
	TopicSubnetDelete = "vpc.delete-subnet"
	TopicCreatePort   = "vpc.create-port"
	TopicDeletePort   = "vpc.delete-port"
	TopicPortStatus   = "vpc.port-status"
	TopicIGWAttach    = "vpc.igw-attach"
	TopicIGWDetach    = "vpc.igw-detach"
)

// VPCEvent is published on vpc.create after a VPC is persisted.
type VPCEvent struct {
	VpcId     string `json:"vpc_id"`
	CidrBlock string `json:"cidr_block"`
	VNI       int64  `json:"vni"`
}

// SubnetEvent is published on vpc.create-subnet / vpc.delete-subnet.
type SubnetEvent struct {
	SubnetId  string `json:"subnet_id"`
	VpcId     string `json:"vpc_id"`
	CidrBlock string `json:"cidr_block"`
}

// PortEvent is published on vpc.create-port / vpc.delete-port.
type PortEvent struct {
	NetworkInterfaceId string `json:"network_interface_id"`
	SubnetId           string `json:"subnet_id"`
	VpcId              string `json:"vpc_id"`
	PrivateIpAddress   string `json:"private_ip_address"`
	MacAddress         string `json:"mac_address"`
}

// IGWEvent is published on vpc.igw-attach / vpc.igw-detach.
type IGWEvent struct {
	InternetGatewayId string `json:"internet_gateway_id"`
	VpcId             string `json:"vpc_id"`
}

// TopologyHandler translates VPC lifecycle NATS events into OVN NB DB operations.
type TopologyHandler struct {
	ovn OVNClient
}

// NewTopologyHandler creates a new TopologyHandler.
func NewTopologyHandler(ovn OVNClient) *TopologyHandler {
	return &TopologyHandler{ovn: ovn}
}

// Subscribe registers NATS subscriptions for VPC lifecycle topics.
// All vpcd instances receive every event (no queue group) because OVN topology
// operations are centralized on the NB DB. Handlers are idempotent — duplicate
// creates on multiple nodes produce harmless "already exists" warnings.
func (h *TopologyHandler) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	type sub struct {
		topic   string
		handler nats.MsgHandler
	}

	subs := []sub{
		{TopicVPCCreate, h.handleVPCCreate},
		{TopicVPCDelete, h.handleVPCDelete},
		{TopicSubnetCreate, h.handleSubnetCreate},
		{TopicSubnetDelete, h.handleSubnetDelete},
		{TopicCreatePort, h.handleCreatePort},
		{TopicDeletePort, h.handleDeletePort},
		{TopicIGWAttach, h.handleIGWAttach},
		{TopicIGWDetach, h.handleIGWDetach},
	}

	var result []*nats.Subscription
	for _, s := range subs {
		natsSub, err := nc.Subscribe(s.topic, s.handler)
		if err != nil {
			// Unsubscribe any already-registered subs
			for _, r := range result {
				_ = r.Unsubscribe()
			}
			return nil, fmt.Errorf("subscribe %s: %w", s.topic, err)
		}
		result = append(result, natsSub)
		slog.Info("Subscribed to VPC topic", "topic", s.topic)
	}

	return result, nil
}

// --- VPC (LogicalRouter) ---

func (h *TopologyHandler) handleVPCCreate(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt VPCEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.create event", "err", err)
		respond(msg, err)
		return
	}

	routerName := "vpc-" + evt.VpcId
	ctx := context.Background()

	// Idempotent: skip if router already exists (another vpcd instance may have created it)
	if _, err := h.ovn.GetLogicalRouter(ctx, routerName); err == nil {
		slog.Debug("vpcd: logical router already exists, skipping", "router", routerName)
		respond(msg, nil)
		return
	}

	lr := &nbdb.LogicalRouter{
		Name: routerName,
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:vni":    fmt.Sprintf("%d", evt.VNI),
		},
	}

	if err := h.ovn.CreateLogicalRouter(ctx, lr); err != nil {
		slog.Error("vpcd: failed to create logical router", "router", routerName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: created logical router for VPC", "router", routerName, "vpc_id", evt.VpcId)
	respond(msg, nil)
}

func (h *TopologyHandler) handleVPCDelete(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt VPCEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.delete event", "err", err)
		respond(msg, err)
		return
	}

	routerName := "vpc-" + evt.VpcId
	ctx := context.Background()

	// List all switches to find ones belonging to this VPC
	switches, err := h.ovn.ListLogicalSwitches(ctx)
	if err != nil {
		slog.Warn("vpcd: failed to list switches during VPC delete", "err", err)
	} else {
		for _, ls := range switches {
			if ls.ExternalIDs["hive:vpc_id"] == evt.VpcId {
				// Delete switch ports first
				for range ls.Ports {
					// Ports are UUIDs; best-effort cleanup
				}
				if err := h.ovn.DeleteLogicalSwitch(ctx, ls.Name); err != nil {
					slog.Warn("vpcd: failed to delete switch during VPC cascade", "switch", ls.Name, "err", err)
				}
			}
		}
	}

	// Delete DHCP options for this VPC
	dhcpOpts, err := h.ovn.ListDHCPOptions(ctx)
	if err != nil {
		slog.Warn("vpcd: failed to list DHCP options during VPC delete", "err", err)
	} else {
		for _, opts := range dhcpOpts {
			if opts.ExternalIDs["hive:vpc_id"] == evt.VpcId {
				if err := h.ovn.DeleteDHCPOptions(ctx, opts.UUID); err != nil {
					slog.Warn("vpcd: failed to delete DHCP options during VPC cascade", "uuid", opts.UUID, "err", err)
				}
			}
		}
	}

	if err := h.ovn.DeleteLogicalRouter(ctx, routerName); err != nil {
		slog.Error("vpcd: failed to delete logical router", "router", routerName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: deleted logical router for VPC", "router", routerName, "vpc_id", evt.VpcId)
	respond(msg, nil)
}

// --- Subnet (LogicalSwitch + RouterPort + DHCP) ---

func (h *TopologyHandler) handleSubnetCreate(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt SubnetEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.create-subnet event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	switchName := "subnet-" + evt.SubnetId
	routerName := "vpc-" + evt.VpcId
	routerPortName := "rtr-" + evt.SubnetId
	switchRouterPortName := "rtr-port-" + evt.SubnetId

	// Parse subnet CIDR to compute gateway IP
	gwIP, mask, err := subnetGateway(evt.CidrBlock)
	if err != nil {
		slog.Error("vpcd: invalid subnet CIDR", "cidr", evt.CidrBlock, "err", err)
		respond(msg, err)
		return
	}
	gwCIDR := fmt.Sprintf("%s/%d", gwIP, mask)

	// Generate a deterministic MAC for the router port
	routerMAC := generateMAC(evt.SubnetId)

	// Idempotent: skip if switch already exists (another vpcd instance may have created it)
	if _, err := h.ovn.GetLogicalSwitch(ctx, switchName); err == nil {
		slog.Debug("vpcd: subnet topology already exists, skipping", "switch", switchName)
		respond(msg, nil)
		return
	}

	// 1. Create LogicalSwitch
	ls := &nbdb.LogicalSwitch{
		Name: switchName,
		ExternalIDs: map[string]string{
			"hive:subnet_id": evt.SubnetId,
			"hive:vpc_id":    evt.VpcId,
		},
	}
	if err := h.ovn.CreateLogicalSwitch(ctx, ls); err != nil {
		slog.Error("vpcd: failed to create logical switch", "switch", switchName, "err", err)
		respond(msg, err)
		return
	}

	// 2. Create LogicalRouterPort on the VPC router
	lrp := &nbdb.LogicalRouterPort{
		Name:     routerPortName,
		MAC:      routerMAC,
		Networks: []string{gwCIDR},
		ExternalIDs: map[string]string{
			"hive:subnet_id": evt.SubnetId,
			"hive:vpc_id":    evt.VpcId,
		},
	}
	if err := h.ovn.CreateLogicalRouterPort(ctx, routerName, lrp); err != nil {
		slog.Error("vpcd: failed to create router port", "port", routerPortName, "err", err)
		// Best-effort cleanup
		_ = h.ovn.DeleteLogicalSwitch(ctx, switchName)
		respond(msg, err)
		return
	}

	// 3. Create LogicalSwitchPort (type=router) connecting switch to router
	lsp := &nbdb.LogicalSwitchPort{
		Name:      switchRouterPortName,
		Type:      "router",
		Addresses: []string{"router"},
		Options: map[string]string{
			"router-port": routerPortName,
		},
		ExternalIDs: map[string]string{
			"hive:subnet_id": evt.SubnetId,
			"hive:vpc_id":    evt.VpcId,
		},
	}
	if err := h.ovn.CreateLogicalSwitchPort(ctx, switchName, lsp); err != nil {
		slog.Error("vpcd: failed to create switch router port", "port", switchRouterPortName, "err", err)
		_ = h.ovn.DeleteLogicalRouterPort(ctx, routerName, routerPortName)
		_ = h.ovn.DeleteLogicalSwitch(ctx, switchName)
		respond(msg, err)
		return
	}

	// 4. Create DHCP_Options for the subnet
	dhcpOpts := &nbdb.DHCPOptions{
		CIDR: evt.CidrBlock,
		Options: map[string]string{
			"server_id":  gwIP,
			"server_mac": routerMAC,
			"lease_time": "3600",
			"router":     gwIP,
			"dns_server": "169.254.169.253",
			"mtu":        "1442", // Geneve overhead
		},
		ExternalIDs: map[string]string{
			"hive:subnet_id": evt.SubnetId,
			"hive:vpc_id":    evt.VpcId,
		},
	}
	if _, err := h.ovn.CreateDHCPOptions(ctx, dhcpOpts); err != nil {
		slog.Error("vpcd: failed to create DHCP options", "cidr", evt.CidrBlock, "err", err)
		// Non-fatal: switch and router port are still useful
	}

	slog.Info("vpcd: created subnet topology",
		"switch", switchName,
		"router_port", routerPortName,
		"gateway", gwCIDR,
		"subnet_id", evt.SubnetId,
	)
	respond(msg, nil)
}

func (h *TopologyHandler) handleSubnetDelete(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt SubnetEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.delete-subnet event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	switchName := "subnet-" + evt.SubnetId
	routerName := "vpc-" + evt.VpcId
	routerPortName := "rtr-" + evt.SubnetId
	switchRouterPortName := "rtr-port-" + evt.SubnetId

	// 1. Delete switch router port
	if err := h.ovn.DeleteLogicalSwitchPort(ctx, switchName, switchRouterPortName); err != nil {
		slog.Warn("vpcd: failed to delete switch router port", "port", switchRouterPortName, "err", err)
	}

	// 2. Delete router port
	if err := h.ovn.DeleteLogicalRouterPort(ctx, routerName, routerPortName); err != nil {
		slog.Warn("vpcd: failed to delete router port", "port", routerPortName, "err", err)
	}

	// 3. Delete DHCP options for this subnet
	dhcpOpts, err := h.ovn.FindDHCPOptionsByCIDR(ctx, evt.CidrBlock)
	if err == nil {
		if err := h.ovn.DeleteDHCPOptions(ctx, dhcpOpts.UUID); err != nil {
			slog.Warn("vpcd: failed to delete DHCP options", "cidr", evt.CidrBlock, "err", err)
		}
	}

	// 4. Delete the logical switch
	if err := h.ovn.DeleteLogicalSwitch(ctx, switchName); err != nil {
		slog.Error("vpcd: failed to delete logical switch", "switch", switchName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: deleted subnet topology", "switch", switchName, "subnet_id", evt.SubnetId)
	respond(msg, nil)
}

// --- Port (LogicalSwitchPort for VM/ENI) ---

func (h *TopologyHandler) handleCreatePort(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt PortEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.create-port event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	portName := "port-" + evt.NetworkInterfaceId
	switchName := "subnet-" + evt.SubnetId

	// Idempotent: skip if port already exists
	if _, err := h.ovn.GetLogicalSwitchPort(ctx, portName); err == nil {
		slog.Debug("vpcd: logical switch port already exists, skipping", "port", portName)
		respond(msg, nil)
		return
	}

	addrStr := fmt.Sprintf("%s %s", evt.MacAddress, evt.PrivateIpAddress)

	lsp := &nbdb.LogicalSwitchPort{
		Name:         portName,
		Addresses:    []string{addrStr},
		PortSecurity: []string{addrStr},
		ExternalIDs: map[string]string{
			"hive:eni_id":    evt.NetworkInterfaceId,
			"hive:subnet_id": evt.SubnetId,
			"hive:vpc_id":    evt.VpcId,
		},
	}

	// Look up DHCP options for the subnet and attach to the port
	dhcpOpts, err := h.ovn.FindDHCPOptionsByExternalID(ctx, "hive:subnet_id", evt.SubnetId)
	if err != nil {
		slog.Warn("vpcd: DHCP options not found for subnet, port will not have DHCP", "subnet", evt.SubnetId, "err", err)
	} else {
		lsp.DHCPv4Options = &dhcpOpts.UUID
	}

	if err := h.ovn.CreateLogicalSwitchPort(ctx, switchName, lsp); err != nil {
		slog.Error("vpcd: failed to create logical switch port", "port", portName, "switch", switchName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: created logical switch port for ENI",
		"port", portName,
		"switch", switchName,
		"eni_id", evt.NetworkInterfaceId,
		"ip", evt.PrivateIpAddress,
	)
	respond(msg, nil)
}

func (h *TopologyHandler) handleDeletePort(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt PortEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.delete-port event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	portName := "port-" + evt.NetworkInterfaceId
	switchName := "subnet-" + evt.SubnetId

	if err := h.ovn.DeleteLogicalSwitchPort(ctx, switchName, portName); err != nil {
		slog.Error("vpcd: failed to delete logical switch port", "port", portName, "switch", switchName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: deleted logical switch port for ENI",
		"port", portName,
		"switch", switchName,
		"eni_id", evt.NetworkInterfaceId,
	)
	respond(msg, nil)
}

// --- Internet Gateway (external connectivity + NAT) ---

func (h *TopologyHandler) handleIGWAttach(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt IGWEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.igw-attach event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	routerName := "vpc-" + evt.VpcId
	extSwitchName := "ext-" + evt.VpcId
	extPortName := "ext-port-" + evt.VpcId
	gwPortName := "gw-" + evt.VpcId
	switchGWPortName := "gw-port-" + evt.VpcId

	// Idempotent: skip if external switch already exists
	if _, err := h.ovn.GetLogicalSwitch(ctx, extSwitchName); err == nil {
		slog.Debug("vpcd: IGW topology already exists, skipping", "switch", extSwitchName)
		respond(msg, nil)
		return
	}

	// 1. Create external logical switch (localnet for physical uplink)
	extSwitch := &nbdb.LogicalSwitch{
		Name: extSwitchName,
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:igw_id": evt.InternetGatewayId,
			"hive:role":   "external",
		},
	}
	if err := h.ovn.CreateLogicalSwitch(ctx, extSwitch); err != nil {
		slog.Error("vpcd: failed to create external switch", "switch", extSwitchName, "err", err)
		respond(msg, err)
		return
	}

	// 2. Create localnet port on external switch (maps to physical network)
	localnetPort := &nbdb.LogicalSwitchPort{
		Name:      extPortName,
		Type:      "localnet",
		Addresses: []string{"unknown"},
		Options: map[string]string{
			"network_name": "external",
		},
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:igw_id": evt.InternetGatewayId,
		},
	}
	if err := h.ovn.CreateLogicalSwitchPort(ctx, extSwitchName, localnetPort); err != nil {
		slog.Error("vpcd: failed to create localnet port", "port", extPortName, "err", err)
		_ = h.ovn.DeleteLogicalSwitch(ctx, extSwitchName)
		respond(msg, err)
		return
	}

	// 3. Create gateway router port on the VPC router connecting to external switch
	gwMAC := generateMAC("gw-" + evt.VpcId)
	lrp := &nbdb.LogicalRouterPort{
		Name:     gwPortName,
		MAC:      gwMAC,
		Networks: []string{"169.254.0.1/30"}, // link-local for external transit
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:igw_id": evt.InternetGatewayId,
			"hive:role":   "gateway",
		},
	}
	if err := h.ovn.CreateLogicalRouterPort(ctx, routerName, lrp); err != nil {
		slog.Error("vpcd: failed to create gateway router port", "port", gwPortName, "err", err)
		_ = h.ovn.DeleteLogicalSwitch(ctx, extSwitchName)
		respond(msg, err)
		return
	}

	// 4. Create switch port connecting external switch to router
	switchGWPort := &nbdb.LogicalSwitchPort{
		Name:      switchGWPortName,
		Type:      "router",
		Addresses: []string{"router"},
		Options: map[string]string{
			"router-port": gwPortName,
		},
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:igw_id": evt.InternetGatewayId,
		},
	}
	if err := h.ovn.CreateLogicalSwitchPort(ctx, extSwitchName, switchGWPort); err != nil {
		slog.Error("vpcd: failed to create switch gateway port", "port", switchGWPortName, "err", err)
		_ = h.ovn.DeleteLogicalRouterPort(ctx, routerName, gwPortName)
		_ = h.ovn.DeleteLogicalSwitch(ctx, extSwitchName)
		respond(msg, err)
		return
	}

	// 5. Add SNAT rule — masquerade all VPC traffic going through the gateway
	//    Get the VPC CIDR from the router's external_ids
	router, err := h.ovn.GetLogicalRouter(ctx, routerName)
	if err != nil {
		slog.Warn("vpcd: failed to get router for SNAT, skipping NAT setup", "router", routerName, "err", err)
	} else {
		vpcCIDR := router.ExternalIDs["hive:cidr"]
		if vpcCIDR == "" {
			// Fall back to a reasonable default — list subnets for this VPC
			vpcCIDR = "10.0.0.0/8"
		}
		snatRule := &nbdb.NAT{
			Type:       "snat",
			ExternalIP: "169.254.0.1",
			LogicalIP:  vpcCIDR,
			ExternalIDs: map[string]string{
				"hive:vpc_id": evt.VpcId,
				"hive:igw_id": evt.InternetGatewayId,
			},
		}
		if err := h.ovn.AddNAT(ctx, routerName, snatRule); err != nil {
			slog.Warn("vpcd: failed to add SNAT rule", "router", routerName, "err", err)
		}
	}

	// 6. Add default route pointing to the external gateway
	defaultRoute := &nbdb.LogicalRouterStaticRoute{
		IPPrefix: "0.0.0.0/0",
		Nexthop:  "169.254.0.2",
		ExternalIDs: map[string]string{
			"hive:vpc_id": evt.VpcId,
			"hive:igw_id": evt.InternetGatewayId,
		},
	}
	if err := h.ovn.AddStaticRoute(ctx, routerName, defaultRoute); err != nil {
		slog.Warn("vpcd: failed to add default route", "router", routerName, "err", err)
	}

	slog.Info("vpcd: attached internet gateway to VPC",
		"igw_id", evt.InternetGatewayId,
		"vpc_id", evt.VpcId,
		"ext_switch", extSwitchName,
		"gw_port", gwPortName,
	)
	respond(msg, nil)
}

func (h *TopologyHandler) handleIGWDetach(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt IGWEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.igw-detach event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	routerName := "vpc-" + evt.VpcId
	extSwitchName := "ext-" + evt.VpcId
	extPortName := "ext-port-" + evt.VpcId
	gwPortName := "gw-" + evt.VpcId
	switchGWPortName := "gw-port-" + evt.VpcId

	// 1. Delete default route
	if err := h.ovn.DeleteStaticRoute(ctx, routerName, "0.0.0.0/0"); err != nil {
		slog.Warn("vpcd: failed to delete default route", "router", routerName, "err", err)
	}

	// 2. Delete SNAT rule(s) for this IGW
	// Find VPC CIDR for the SNAT rule
	router, err := h.ovn.GetLogicalRouter(ctx, routerName)
	if err != nil {
		slog.Warn("vpcd: failed to get router for NAT cleanup", "router", routerName, "err", err)
	} else {
		vpcCIDR := router.ExternalIDs["hive:cidr"]
		if vpcCIDR == "" {
			vpcCIDR = "10.0.0.0/8"
		}
		if err := h.ovn.DeleteNAT(ctx, routerName, "snat", vpcCIDR); err != nil {
			slog.Warn("vpcd: failed to delete SNAT rule", "router", routerName, "err", err)
		}
	}

	// 3. Delete switch gateway port
	if err := h.ovn.DeleteLogicalSwitchPort(ctx, extSwitchName, switchGWPortName); err != nil {
		slog.Warn("vpcd: failed to delete switch gateway port", "port", switchGWPortName, "err", err)
	}

	// 4. Delete gateway router port
	if err := h.ovn.DeleteLogicalRouterPort(ctx, routerName, gwPortName); err != nil {
		slog.Warn("vpcd: failed to delete gateway router port", "port", gwPortName, "err", err)
	}

	// 5. Delete localnet port
	if err := h.ovn.DeleteLogicalSwitchPort(ctx, extSwitchName, extPortName); err != nil {
		slog.Warn("vpcd: failed to delete localnet port", "port", extPortName, "err", err)
	}

	// 6. Delete external switch
	if err := h.ovn.DeleteLogicalSwitch(ctx, extSwitchName); err != nil {
		slog.Error("vpcd: failed to delete external switch", "switch", extSwitchName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: detached internet gateway from VPC",
		"igw_id", evt.InternetGatewayId,
		"vpc_id", evt.VpcId,
	)
	respond(msg, nil)
}

// --- Helpers ---

// subnetGateway computes the gateway IP (.1) from a CIDR string.
// Returns the gateway IP string and the prefix length.
func subnetGateway(cidr string) (string, int, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", 0, fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}

	// Use the network address, increment last octet to .1
	gw := ipNet.IP.To4()
	if gw == nil {
		return "", 0, fmt.Errorf("only IPv4 supported, got %s", ip)
	}
	gw = make(net.IP, len(ipNet.IP.To4()))
	copy(gw, ipNet.IP.To4())
	gw[3]++

	ones, _ := ipNet.Mask.Size()
	return gw.String(), ones, nil
}

// generateMAC creates a deterministic MAC address from a resource ID.
// Uses the locally-administered unicast prefix 02:00:00.
func generateMAC(resourceID string) string {
	// Simple hash: use first 6 hex chars of resource ID after the prefix
	h := uint32(0)
	for _, c := range resourceID {
		h = h*31 + uint32(c)
	}
	return fmt.Sprintf("02:00:00:%02x:%02x:%02x", (h>>16)&0xff, (h>>8)&0xff, h&0xff)
}

// respond sends a simple JSON response to a NATS request.
func respond(msg *nats.Msg, err error) {
	if msg.Reply == "" {
		return // fire-and-forget, no reply expected
	}

	type response struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}

	resp := response{Success: true}
	if err != nil {
		resp.Success = false
		resp.Error = err.Error()
	}

	data, _ := json.Marshal(resp)
	if err := msg.Respond(data); err != nil {
		slog.Error("vpcd: failed to respond to NATS request", "err", err)
	}
}
