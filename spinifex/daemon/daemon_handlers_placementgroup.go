package daemon

import "github.com/nats-io/nats.go"

func (d *Daemon) handleEC2CreatePlacementGroup(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.CreatePlacementGroup)
}

func (d *Daemon) handleEC2DeletePlacementGroup(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.DeletePlacementGroup)
}

func (d *Daemon) handleEC2DescribePlacementGroups(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.DescribePlacementGroups)
}
