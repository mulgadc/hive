package types

import "sync"

type EBSRequests struct {
	Requests []EBSRequest `mapstructure:"ebs_requests"`
	Mu       sync.Mutex   `json:"-"`
}

type EBSRequest struct {
	Name                string
	VolType             string
	Boot                bool
	EFI                 bool
	CloudInit           bool
	DeleteOnTermination bool
	NBDURI              string // NBD URI - socket path (nbd:unix:/path.sock) or TCP (nbd://host:port)
	DeviceName          string // AWS API device name (e.g. /dev/sdf) for hot-plugged volumes
}

// NBDTransport defines the transport type for NBD connections
type NBDTransport string

const (
	// NBDTransportSocket uses Unix domain sockets (faster, local only)
	NBDTransportSocket NBDTransport = "socket"
	// NBDTransportTCP uses TCP connections (required for remote/DPU scenarios)
	NBDTransportTCP NBDTransport = "tcp"
)

type EBSMountResponse struct {
	URI     string
	Mounted bool
	Error   string
}

type EBSUnMountResponse struct {
	Volume  string
	Mounted bool
	Error   string
}

type EBSSyncRequest struct {
	Volume string
}

type EBSSyncResponse struct {
	Volume string
	Synced bool
	Error  string
}

type EBSDeleteRequest struct {
	Volume string
}

type EBSDeleteResponse struct {
	Volume  string
	Success bool
	Error   string
}

type EBSSnapshotRequest struct {
	Volume     string
	SnapshotID string
}

type EBSSnapshotResponse struct {
	SnapshotID string
	Success    bool
	Error      string
}
