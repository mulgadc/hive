package types

// NodeDiscoverResponse is the response for node discovery requests.
type NodeDiscoverResponse struct {
	Node string `json:"node"`
}

// NodeStatusResponse is returned by the hive.node.status NATS topic (fan-out).
type NodeStatusResponse struct {
	Node          string            `json:"node"`
	Status        string            `json:"status"`
	Host          string            `json:"host"`
	Region        string            `json:"region"`
	AZ            string            `json:"az"`
	Uptime        int64             `json:"uptime"`
	Services      []string          `json:"services"`
	TotalVCPU     int               `json:"total_vcpu"`
	TotalMemGB    float64           `json:"total_mem_gb"`
	AllocVCPU     int               `json:"alloc_vcpu"`
	AllocMemGB    float64           `json:"alloc_mem_gb"`
	VMCount       int               `json:"vm_count"`
	InstanceTypes []InstanceTypeCap `json:"instance_types"`
}

// InstanceTypeCap describes available capacity for one instance type on a node.
type InstanceTypeCap struct {
	Name      string  `json:"name"`
	VCPU      int     `json:"vcpu"`
	MemoryGB  float64 `json:"memory_gb"`
	Available int     `json:"available"`
}

// VMInfo describes a single VM for the cluster stats CLI.
type VMInfo struct {
	InstanceID   string  `json:"instance_id"`
	Status       string  `json:"status"`
	InstanceType string  `json:"instance_type"`
	VCPU         int     `json:"vcpu"`
	MemoryGB     float64 `json:"memory_gb"`
	LaunchTime   int64   `json:"launch_time"`
}

// NodeVMsResponse is returned by the hive.node.vms NATS topic (fan-out).
type NodeVMsResponse struct {
	Node string   `json:"node"`
	Host string   `json:"host"`
	VMs  []VMInfo `json:"vms"`
}
