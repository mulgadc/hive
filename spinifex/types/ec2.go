package types

// EC2InstanceCommand is the NATS wire format for EC2 instance commands
// (stop, terminate, start, attach-volume, detach-volume).
// It replaces direct use of qmp.Command on the gateway→daemon boundary.
type EC2InstanceCommand struct {
	ID               string               `json:"id"`
	Attributes       EC2CommandAttributes `json:"attributes"`
	AttachVolumeData *AttachVolumeData    `json:"attach_volume_data,omitempty"`
	DetachVolumeData *DetachVolumeData    `json:"detach_volume_data,omitempty"`
}

// EC2CommandAttributes indicates which action the daemon should perform.
type EC2CommandAttributes struct {
	StopInstance      bool `json:"stop_instance"`
	TerminateInstance bool `json:"delete_instance"`
	StartInstance     bool `json:"start_instance"`
	AttachVolume      bool `json:"attach_volume"`
	DetachVolume      bool `json:"detach_volume"`
	RebootInstance    bool `json:"reboot_instance"`
}

// AttachVolumeData carries parameters for an attach-volume command.
type AttachVolumeData struct {
	VolumeID string `json:"volume_id"`
	Device   string `json:"device,omitempty"`
}

// DetachVolumeData carries parameters for a detach-volume command.
type DetachVolumeData struct {
	VolumeID string `json:"volume_id"`
	Device   string `json:"device,omitempty"`
	Force    bool   `json:"force,omitempty"`
}
