package daemon

import (
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2EnableEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.EnableEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2DisableEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.DisableEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2GetEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.GetEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2GetSerialConsoleAccessStatus(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.GetSerialConsoleAccessStatus)
}

func (d *Daemon) handleEC2EnableSerialConsoleAccess(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.EnableSerialConsoleAccess)
}

func (d *Daemon) handleEC2DisableSerialConsoleAccess(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.DisableSerialConsoleAccess)
}
