package handlers_elbv2

// SystemInstanceLauncher is the subset of daemon functionality needed by
// ELBv2 to launch and terminate system-managed VMs (e.g. ALB VMs).
// Defined here to avoid a circular import between handlers/elbv2 and daemon.
//
// System instances are owned by the system account and hidden from
// customer DescribeInstances calls.
type SystemInstanceLauncher interface {
	// LaunchSystemInstance creates and starts a system-managed VM.
	// The VM is assigned to the system account and not visible to customers.
	// The caller provides the instance type, AMI, subnet, user data, and
	// optionally a pre-created ENI to attach.
	//
	// Returns the instance ID and private IP once the VM is running.
	LaunchSystemInstance(input *SystemInstanceInput) (*SystemInstanceOutput, error)

	// TerminateSystemInstance stops and cleans up a system-managed VM.
	TerminateSystemInstance(instanceID string) error
}

// SystemInstanceInput describes the VM to launch.
type SystemInstanceInput struct {
	InstanceType string // e.g. "t3.nano"
	ImageID      string // AMI ID
	SubnetID     string // VPC subnet for networking
	UserData     string // Cloud-init user data (plain text, will be base64-encoded)

	// ENI fields — if ENIID is set, the VM uses this pre-created ENI instead
	// of auto-creating one. Used for ALB VMs that reuse the ALB's ENI.
	ENIID  string
	ENIMac string
	ENIIP  string

	// HostfwdPorts specifies additional guest ports to forward from the host
	// via the QEMU user-mode dev NIC (dev_networking mode only).
	// Each entry is a guest port (e.g. 80, 443). The host port is auto-assigned.
	HostfwdPorts []int
}

// SystemInstanceOutput contains the result of a successful launch.
type SystemInstanceOutput struct {
	InstanceID string // e.g. "i-xxxxx"
	PrivateIP  string // VPC private IP

	// HostfwdMap maps guest port → host port for any forwarded ports.
	// Only populated when dev_networking is enabled and HostfwdPorts were requested.
	HostfwdMap map[int]int
}
