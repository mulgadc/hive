package types

import "github.com/mulgadc/hive/hive/config"

// Cluster manager API types

type NodeJoinRequest struct {
	Node       string `json:"node"`
	Region     string `json:"region"`
	AZ         string `json:"az"`
	DataDir    string `json:"data_dir"`
	DaemonHost string `json:"daemon_host"` // Host:port where this node's daemon is accessible
}

// SharedClusterData contains only the shared cluster information (no node-specific top-level fields)
type SharedClusterData struct {
	Epoch   uint64                   `json:"epoch" toml:"epoch"`
	Version string                   `json:"version" toml:"version"`
	Nodes   map[string]config.Config `json:"nodes" toml:"nodes"`
}

type NodeJoinResponse struct {
	Success     bool               `json:"success"`
	Message     string             `json:"message"`
	SharedData  *SharedClusterData `json:"shared_data,omitempty"`
	ConfigHash  string             `json:"config_hash,omitempty"`
	JoiningNode string             `json:"joining_node,omitempty"` // The node name that is joining
	// CA distribution for per-node certificate generation
	CACert string `json:"ca_cert,omitempty"` // PEM-encoded CA certificate
	CAKey  string `json:"ca_key,omitempty"`  // PEM-encoded CA private key
	// Predastore config distribution for multi-node clusters
	PredastoreConfig string `json:"predastore_config,omitempty"` // Full predastore.toml content
}

type NodeHealthResponse struct {
	Node          string            `json:"node"`
	Status        string            `json:"status"`
	ConfigHash    string            `json:"config_hash"`
	Epoch         uint64            `json:"epoch"`
	Uptime        int64             `json:"uptime"`
	Services      []string          `json:"services"`
	ServiceHealth map[string]string `json:"service_health,omitempty"`
}
