package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateSnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.CreateSnapshot)
}

func (d *Daemon) handleEC2DescribeSnapshots(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.DescribeSnapshots)
}

func (d *Daemon) handleEC2DeleteSnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.DeleteSnapshot)
}

func (d *Daemon) handleEC2CopySnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.CopySnapshot)
}
