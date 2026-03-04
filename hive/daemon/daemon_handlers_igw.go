package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.igwService.CreateInternetGateway)
}

func (d *Daemon) handleEC2DeleteInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.igwService.DeleteInternetGateway)
}

func (d *Daemon) handleEC2DescribeInternetGateways(msg *nats.Msg) {
	handleNATSRequest(msg, d.igwService.DescribeInternetGateways)
}

func (d *Daemon) handleEC2AttachInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.igwService.AttachInternetGateway)
}

func (d *Daemon) handleEC2DetachInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.igwService.DetachInternetGateway)
}

func (d *Daemon) handleEC2CreateEgressOnlyInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.CreateEgressOnlyInternetGateway)
}

func (d *Daemon) handleEC2DeleteEgressOnlyInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.DeleteEgressOnlyInternetGateway)
}

func (d *Daemon) handleEC2DescribeEgressOnlyInternetGateways(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.DescribeEgressOnlyInternetGateways)
}
