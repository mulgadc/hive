package daemon

import (
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
