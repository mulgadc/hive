package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateKeyPair(msg *nats.Msg) {
	handleNATSRequestWithAccount(msg, d.keyService.CreateKeyPair)
}

func (d *Daemon) handleEC2DeleteKeyPair(msg *nats.Msg) {
	handleNATSRequestWithAccount(msg, d.keyService.DeleteKeyPair)
}

func (d *Daemon) handleEC2DescribeKeyPairs(msg *nats.Msg) {
	handleNATSRequestWithAccount(msg, d.keyService.DescribeKeyPairs)
}

func (d *Daemon) handleEC2ImportKeyPair(msg *nats.Msg) {
	handleNATSRequestWithAccount(msg, d.keyService.ImportKeyPair)
}
