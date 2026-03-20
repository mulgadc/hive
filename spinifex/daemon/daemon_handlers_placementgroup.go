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

func (d *Daemon) handleEC2ReserveSpreadNodes(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.ReserveSpreadNodes)
}

func (d *Daemon) handleEC2FinalizeSpreadInstances(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.FinalizeSpreadInstances)
}

func (d *Daemon) handleEC2ReleaseSpreadNodes(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.ReleaseSpreadNodes)
}

func (d *Daemon) handleEC2RemoveInstanceFromPlacementGroup(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.RemoveInstance)
}

func (d *Daemon) handleEC2ReserveClusterNode(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.ReserveClusterNode)
}

func (d *Daemon) handleEC2FinalizeClusterInstances(msg *nats.Msg) {
	handleNATSRequest(msg, d.placementGroupService.FinalizeClusterInstances)
}
