package daemon

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

const (
	// InstanceStateBucket is the name of the KV bucket for storing instance state
	InstanceStateBucket = "hive-instance-state"
	// InstanceStatePrefix is the key prefix for instance state entries
	InstanceStatePrefix = "node."
)

// JetStreamManager manages JetStream KV store operations for instance state
type JetStreamManager struct {
	js       nats.JetStreamContext
	kv       nats.KeyValue
	replicas int
}

// NewJetStreamManager creates a new JetStreamManager from a NATS connection.
// replicas specifies the number of replicas for the KV bucket (typically matches cluster node count).
func NewJetStreamManager(nc *nats.Conn, replicas int) (*JetStreamManager, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, err
	}

	return &JetStreamManager{
		js:       js,
		replicas: replicas,
	}, nil
}

// InitKVBucket initializes the KV bucket, creating it if it doesn't exist
func (m *JetStreamManager) InitKVBucket() error {
	// Try to get the existing bucket first
	kv, err := m.js.KeyValue(InstanceStateBucket)
	if err != nil {
		if errors.Is(err, nats.ErrBucketNotFound) {
			// Bucket doesn't exist, create it
			slog.Info("Creating JetStream KV bucket", "bucket", InstanceStateBucket, "replicas", m.replicas)
			kv, err = m.js.CreateKeyValue(&nats.KeyValueConfig{
				Bucket:      InstanceStateBucket,
				Description: "Hive instance state storage",
				History:     1,          // Only keep latest value
				Replicas:    m.replicas, // Replication across cluster nodes
			})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		slog.Info("Connected to existing JetStream KV bucket", "bucket", InstanceStateBucket)
	}

	m.kv = kv
	return nil
}

// WriteState writes the instance state to the KV store for the given node.
// It acquires instances.Mu internally.
func (m *JetStreamManager) WriteState(nodeID string, instances *vm.Instances) error {
	instances.Mu.Lock()
	defer instances.Mu.Unlock()
	return m.writeStateLocked(nodeID, instances)
}

// writeStateLocked writes instance state without acquiring instances.Mu.
// The caller must hold instances.Mu.
func (m *JetStreamManager) writeStateLocked(nodeID string, instances *vm.Instances) error {
	if m.kv == nil {
		return errors.New("KV bucket not initialized")
	}

	// Create a struct without the mutex to avoid copying the lock
	state := struct {
		VMS map[string]*vm.VM `json:"vms"`
	}{
		VMS: instances.VMS,
	}

	jsonData, err := json.Marshal(state)
	if err != nil {
		return err
	}

	key := InstanceStatePrefix + nodeID
	_, err = m.kv.Put(key, jsonData)
	if err != nil {
		return err
	}

	slog.Debug("Wrote state to JetStream KV", "key", key, "instances", len(instances.VMS))
	return nil
}

// LoadState loads the instance state from the KV store for the given node
func (m *JetStreamManager) LoadState(nodeID string) (*vm.Instances, error) {
	if m.kv == nil {
		return nil, errors.New("KV bucket not initialized")
	}

	key := InstanceStatePrefix + nodeID
	entry, err := m.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			// Key doesn't exist, return empty state
			slog.Debug("No existing state in JetStream KV, returning empty state", "key", key)
			return &vm.Instances{VMS: make(map[string]*vm.VM)}, nil
		}
		return nil, err
	}

	var instances vm.Instances
	if err := json.Unmarshal(entry.Value(), &instances); err != nil {
		return nil, err
	}

	// Ensure the VMS map is initialized
	if instances.VMS == nil {
		instances.VMS = make(map[string]*vm.VM)
	}

	slog.Debug("Loaded state from JetStream KV", "key", key, "instances", len(instances.VMS))
	return &instances, nil
}

// DeleteState removes the instance state from the KV store for the given node
func (m *JetStreamManager) DeleteState(nodeID string) error {
	if m.kv == nil {
		return errors.New("KV bucket not initialized")
	}

	key := InstanceStatePrefix + nodeID
	err := m.kv.Delete(key)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}

	slog.Debug("Deleted state from JetStream KV", "key", key)
	return nil
}

// UpdateReplicas updates the replica count for the KV bucket's underlying stream.
// This should be called when new nodes join the cluster.
// Note: Increasing replicas requires the new replica count of NATS servers to be available.
func (m *JetStreamManager) UpdateReplicas(newReplicas int) error {
	if m.js == nil {
		return errors.New("JetStream context not initialized")
	}

	// KV buckets are backed by streams with name "KV_<bucket>"
	streamName := "KV_" + InstanceStateBucket

	// Get current stream info
	streamInfo, err := m.js.StreamInfo(streamName)
	if err != nil {
		return err
	}

	currentReplicas := streamInfo.Config.Replicas
	if currentReplicas >= newReplicas {
		slog.Debug("Stream already has sufficient replicas", "current", currentReplicas, "requested", newReplicas)
		return nil
	}

	// Update the stream config with new replica count
	streamInfo.Config.Replicas = newReplicas

	_, err = m.js.UpdateStream(&streamInfo.Config)
	if err != nil {
		return err
	}

	m.replicas = newReplicas
	slog.Info("Updated JetStream KV bucket replicas", "bucket", InstanceStateBucket, "oldReplicas", currentReplicas, "newReplicas", newReplicas)
	return nil
}
