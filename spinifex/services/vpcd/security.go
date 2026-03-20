package vpcd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// NATS topics for security group lifecycle events.
const (
	TopicCreateSG = "vpc.create-sg"
	TopicDeleteSG = "vpc.delete-sg"
	TopicUpdateSG = "vpc.update-sg"
)

// SGEvent carries security group state from the handler to vpcd.
type SGEvent struct {
	GroupId      string         `json:"group_id"`
	VpcId        string         `json:"vpc_id"`
	IngressRules []SGRuleForACL `json:"ingress_rules,omitempty"`
	EgressRules  []SGRuleForACL `json:"egress_rules,omitempty"`
}

// handleCreateSG creates an OVN Port Group and initial ACLs for a new security group.
func (h *TopologyHandler) handleCreateSG(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt SGEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.create-sg event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	pgName := portGroupName(evt.GroupId)

	// Create port group (initially empty — ports are added when ENIs are assigned to the SG)
	if err := h.ovn.CreatePortGroup(ctx, pgName, nil); err != nil {
		slog.Error("vpcd: failed to create port group", "pg", pgName, "err", err)
		respond(msg, err)
		return
	}

	// Add default deny ACL (priority 900) — drop all traffic not explicitly allowed
	if err := h.ovn.AddACL(ctx, pgName, "to-lport", 900, fmt.Sprintf("outport == @%s && ip4", pgName), "drop"); err != nil {
		slog.Warn("vpcd: failed to add default deny ingress ACL", "pg", pgName, "err", err)
	}
	if err := h.ovn.AddACL(ctx, pgName, "from-lport", 900, fmt.Sprintf("inport == @%s && ip4", pgName), "drop"); err != nil {
		slog.Warn("vpcd: failed to add default deny egress ACL", "pg", pgName, "err", err)
	}

	// Add ACLs for initial rules (priority 1000 — higher than deny)
	h.addRuleACLs(ctx, pgName, evt.IngressRules, evt.EgressRules)

	slog.Info("vpcd: created security group port group",
		"pg", pgName,
		"group_id", evt.GroupId,
		"vpc_id", evt.VpcId,
		"ingress_rules", len(evt.IngressRules),
		"egress_rules", len(evt.EgressRules),
	)
	respond(msg, nil)
}

// handleDeleteSG deletes the OVN Port Group and all associated ACLs.
func (h *TopologyHandler) handleDeleteSG(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt SGEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.delete-sg event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	pgName := portGroupName(evt.GroupId)

	// Clear all ACLs before deleting the port group
	if err := h.ovn.ClearACLs(ctx, pgName); err != nil {
		slog.Warn("vpcd: failed to clear ACLs", "pg", pgName, "err", err)
	}

	if err := h.ovn.DeletePortGroup(ctx, pgName); err != nil {
		slog.Error("vpcd: failed to delete port group", "pg", pgName, "err", err)
		respond(msg, err)
		return
	}

	slog.Info("vpcd: deleted security group port group",
		"pg", pgName,
		"group_id", evt.GroupId,
		"vpc_id", evt.VpcId,
	)
	respond(msg, nil)
}

// handleUpdateSG replaces all ACLs for a security group with the current rule set.
func (h *TopologyHandler) handleUpdateSG(msg *nats.Msg) {
	if h.ovn == nil {
		respond(msg, fmt.Errorf("OVN client not connected"))
		return
	}

	var evt SGEvent
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("vpcd: failed to unmarshal vpc.update-sg event", "err", err)
		respond(msg, err)
		return
	}

	ctx := context.Background()
	pgName := portGroupName(evt.GroupId)

	// Clear existing ACLs
	if err := h.ovn.ClearACLs(ctx, pgName); err != nil {
		slog.Warn("vpcd: failed to clear ACLs for update", "pg", pgName, "err", err)
	}

	// Re-add default deny ACLs (priority 900)
	if err := h.ovn.AddACL(ctx, pgName, "to-lport", 900, fmt.Sprintf("outport == @%s && ip4", pgName), "drop"); err != nil {
		slog.Warn("vpcd: failed to re-add default deny ingress ACL", "pg", pgName, "err", err)
	}
	if err := h.ovn.AddACL(ctx, pgName, "from-lport", 900, fmt.Sprintf("inport == @%s && ip4", pgName), "drop"); err != nil {
		slog.Warn("vpcd: failed to re-add default deny egress ACL", "pg", pgName, "err", err)
	}

	// Add ACLs for current rules
	h.addRuleACLs(ctx, pgName, evt.IngressRules, evt.EgressRules)

	slog.Info("vpcd: updated security group ACLs",
		"pg", pgName,
		"group_id", evt.GroupId,
		"vpc_id", evt.VpcId,
		"ingress_rules", len(evt.IngressRules),
		"egress_rules", len(evt.EgressRules),
	)
	respond(msg, nil)
}

// addRuleACLs adds OVN ACLs for a set of ingress and egress rules at priority 1000
// (higher than the default deny at 900, so allow rules take precedence).
func (h *TopologyHandler) addRuleACLs(ctx context.Context, pgName string, ingress, egress []SGRuleForACL) {
	for _, rule := range ingress {
		match := BuildIngressACLMatch(pgName, rule)
		if err := h.ovn.AddACL(ctx, pgName, "to-lport", 1000, match, "allow-related"); err != nil {
			slog.Warn("vpcd: failed to add ingress ACL", "pg", pgName, "match", match, "err", err)
		}
	}

	for _, rule := range egress {
		match := BuildEgressACLMatch(pgName, rule)
		if err := h.ovn.AddACL(ctx, pgName, "from-lport", 1000, match, "allow-related"); err != nil {
			slog.Warn("vpcd: failed to add egress ACL", "pg", pgName, "match", match, "err", err)
		}
	}
}
