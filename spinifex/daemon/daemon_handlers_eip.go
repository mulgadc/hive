package daemon

import "github.com/nats-io/nats.go"

func (d *Daemon) handleEC2AllocateAddress(msg *nats.Msg) {
	handleNATSRequest(msg, d.eipService.AllocateAddress)
}

func (d *Daemon) handleEC2ReleaseAddress(msg *nats.Msg) {
	handleNATSRequest(msg, d.eipService.ReleaseAddress)
}

func (d *Daemon) handleEC2AssociateAddress(msg *nats.Msg) {
	handleNATSRequest(msg, d.eipService.AssociateAddress)
}

func (d *Daemon) handleEC2DisassociateAddress(msg *nats.Msg) {
	handleNATSRequest(msg, d.eipService.DisassociateAddress)
}

func (d *Daemon) handleEC2DescribeAddresses(msg *nats.Msg) {
	handleNATSRequest(msg, d.eipService.DescribeAddresses)
}
