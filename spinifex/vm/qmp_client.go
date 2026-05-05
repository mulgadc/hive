package vm

import "github.com/mulgadc/spinifex/spinifex/qmp"

// QMPClientFactory connects a QMP client to a VM's QMP socket and runs the
// qmp_capabilities handshake. The returned client is ready for command
// dispatch. Heartbeats are owned by the manager, not the factory.
type QMPClientFactory interface {
	Create(v *VM) (*qmp.QMPClient, error)
}
