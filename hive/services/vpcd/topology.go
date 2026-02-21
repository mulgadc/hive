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

// TopologyHandler translates VPC lifecycle NATS events into OVN NB DB operations.
type TopologyHandler struct {
	ovn OVNClient
}

// NewTopologyHandler creates a new TopologyHandler.
func NewTopologyHandler(ovn OVNClient) *TopologyHandler {
	return &TopologyHandler{ovn: ovn}
}

// Subscribe registers NATS subscriptions for VPC lifecycle topics.
// Uses queue group "vpcd-workers" for load balancing across vpcd instances.
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
	}

	var result []*nats.Subscription
	for _, s := range subs {
		natsSub, err := nc.QueueSubscribe(s.topic, "vpcd-workers", s.handler)
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
