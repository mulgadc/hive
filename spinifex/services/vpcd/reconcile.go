package vpcd

import (
	"context"
	"log/slog"
)

// ReconcileResult tracks what was created during reconciliation.
type ReconcileResult struct {
	RoutersCreated int
	SwitchesCreated int
	IGWsCreated     int
}

// Reconcile ensures OVN topology matches the expected state from the bootstrap config.
// This runs on vpcd startup before subscribing to NATS topics.
//
// Pass 1 (bootstrap): Uses [bootstrap] from spinifex.toml to create the default VPC
// topology. This covers first-install where admin init ran before services started.
//
// All operations are idempotent — safe to call on every startup.
func Reconcile(ctx context.Context, topo *TopologyHandler, bootstrap *BootstrapVPC) ReconcileResult {
	var result ReconcileResult

	if bootstrap == nil || bootstrap.VpcId == "" {
		slog.Debug("vpcd reconcile: no bootstrap config, skipping")
		return result
	}

	slog.Info("vpcd reconcile: checking bootstrap VPC topology",
		"vpc_id", bootstrap.VpcId,
		"subnet_id", bootstrap.SubnetId,
	)

	// 1. Ensure VPC router exists
	routerName := "vpc-" + bootstrap.VpcId
	if _, err := topo.ovn.GetLogicalRouter(ctx, routerName); err != nil {
		slog.Info("vpcd reconcile: creating VPC router", "router", routerName)
		if err := topo.reconcileVPC(ctx, bootstrap.VpcId, bootstrap.Cidr); err != nil {
			slog.Error("vpcd reconcile: failed to create VPC router", "err", err)
		} else {
			result.RoutersCreated++
		}
	} else {
		slog.Debug("vpcd reconcile: VPC router exists", "router", routerName)
	}

	// 2. Ensure subnet switch + router port + DHCP exists
	if bootstrap.SubnetId != "" {
		switchName := "subnet-" + bootstrap.SubnetId
		if _, err := topo.ovn.GetLogicalSwitch(ctx, switchName); err != nil {
			slog.Info("vpcd reconcile: creating subnet topology", "switch", switchName)
			if err := topo.reconcileSubnet(ctx, bootstrap.SubnetId, bootstrap.VpcId, bootstrap.SubnetCidr); err != nil {
				slog.Error("vpcd reconcile: failed to create subnet topology", "err", err)
			} else {
				result.SwitchesCreated++
			}
		} else {
			slog.Debug("vpcd reconcile: subnet switch exists", "switch", switchName)
		}
	}

	// 3. Ensure IGW topology exists (external switch, SNAT, gateway chassis)
	extSwitchName := "ext-" + bootstrap.VpcId
	if _, err := topo.ovn.GetLogicalSwitch(ctx, extSwitchName); err != nil {
		slog.Info("vpcd reconcile: creating IGW topology", "switch", extSwitchName)
		if err := topo.reconcileIGW(ctx, bootstrap.VpcId); err != nil {
			slog.Error("vpcd reconcile: failed to create IGW topology", "err", err)
		} else {
			result.IGWsCreated++
		}
	} else {
		slog.Debug("vpcd reconcile: IGW topology exists", "switch", extSwitchName)
	}

	slog.Info("vpcd reconcile: complete",
		"routers_created", result.RoutersCreated,
		"switches_created", result.SwitchesCreated,
		"igws_created", result.IGWsCreated,
	)

	return result
}
