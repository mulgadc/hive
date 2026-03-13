package daemon

import (
	"encoding/json"
	"log/slog"

	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateVpc(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateVpc)
}

func (d *Daemon) handleEC2DeleteVpc(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteVpc)
}

func (d *Daemon) handleEC2DescribeVpcs(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeVpcs)
}

func (d *Daemon) handleEC2CreateSubnet(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateSubnet)
}

func (d *Daemon) handleEC2DeleteSubnet(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteSubnet)
}

func (d *Daemon) handleEC2DescribeSubnets(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeSubnets)
}

func (d *Daemon) handleEC2CreateNetworkInterface(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateNetworkInterface)
}

func (d *Daemon) handleEC2DeleteNetworkInterface(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteNetworkInterface)
}

func (d *Daemon) handleEC2DescribeNetworkInterfaces(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeNetworkInterfaces)
}

// handleAccountCreated creates a default VPC for a newly created account.
func (d *Daemon) handleAccountCreated(msg *nats.Msg) {
	var evt struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("Failed to unmarshal account creation event", "error", err)
		return
	}
	if evt.AccountID == "" {
		slog.Error("Account creation event has empty account ID")
		return
	}
	if err := d.vpcService.EnsureDefaultVPC(evt.AccountID); err != nil {
		slog.Error("Failed to create default VPC for new account",
			"accountID", evt.AccountID, "error", err)
	}
}
