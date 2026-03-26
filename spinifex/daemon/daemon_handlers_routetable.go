package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateRouteTable(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.CreateRouteTable)
}

func (d *Daemon) handleEC2DeleteRouteTable(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.DeleteRouteTable)
}

func (d *Daemon) handleEC2DescribeRouteTables(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.DescribeRouteTables)
}

func (d *Daemon) handleEC2CreateRoute(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.CreateRoute)
}

func (d *Daemon) handleEC2DeleteRoute(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.DeleteRoute)
}

func (d *Daemon) handleEC2ReplaceRoute(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.ReplaceRoute)
}

func (d *Daemon) handleEC2AssociateRouteTable(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.AssociateRouteTable)
}

func (d *Daemon) handleEC2DisassociateRouteTable(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.DisassociateRouteTable)
}

func (d *Daemon) handleEC2ReplaceRouteTableAssociation(msg *nats.Msg) {
	handleNATSRequest(msg, d.routeTableService.ReplaceRouteTableAssociation)
}
