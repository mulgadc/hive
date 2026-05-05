package vm

// NetworkPlumber handles tap device and OVS bridge operations for VPC
// networking. Defined here so the manager can hold the collaborator without
// importing the daemon package; the daemon's OVSNetworkPlumber type satisfies
// it structurally.
type NetworkPlumber interface {
	SetupTapDevice(eniID, mac string) error
	CleanupTapDevice(eniID string) error
}
