package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.CreateTags)
}

func (d *Daemon) handleEC2DeleteTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DeleteTags)
}

func (d *Daemon) handleEC2DescribeTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DescribeTags)
}
