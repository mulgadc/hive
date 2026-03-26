package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateNatGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.natGatewayService.CreateNatGateway)
}

func (d *Daemon) handleEC2DeleteNatGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.natGatewayService.DeleteNatGateway)
}

func (d *Daemon) handleEC2DescribeNatGateways(msg *nats.Msg) {
	handleNATSRequest(msg, d.natGatewayService.DescribeNatGateways)
}
