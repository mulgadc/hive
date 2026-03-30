package types

// StorageConfigResponse is returned by the spinifex.storage.config NATS topic.
// Contains predastore topology with credentials stripped.
type StorageConfigResponse struct {
	Encoding   StorageEncoding    `json:"encoding"`
	DBNodes    []StorageDBNode    `json:"db_nodes"`
	ShardNodes []StorageShardNode `json:"shard_nodes"`
	Buckets    []StorageBucket    `json:"buckets"`
}

// StorageEncoding describes the Reed-Solomon erasure coding configuration.
type StorageEncoding struct {
	DataShards   int `json:"data_shards"`
	ParityShards int `json:"parity_shards"`
}

// StorageDBNode describes a predastore distributed database node (no credentials).
type StorageDBNode struct {
	ID   int    `json:"id"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// StorageShardNode describes a predastore object storage shard node.
type StorageShardNode struct {
	ID   int    `json:"id"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// StorageBucket describes a configured S3 bucket.
type StorageBucket struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Region string `json:"region"`
}
