package daemon

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

const (
	// InstanceStateBucket is the name of the KV bucket for storing instance state
	InstanceStateBucket = "hive-instance-state"
	// ClusterStateBucket is the name of the KV bucket for cluster state (heartbeats, shutdown markers, service maps)
	ClusterStateBucket = "hive-cluster-state"
	// InstanceStatePrefix is the key prefix for per-node instance state entries
	InstanceStatePrefix = "node."
	// StoppedInstancePrefix is the key prefix for stopped instances in shared KV
	StoppedInstancePrefix = "instance."
)

// JetStreamManager manages JetStream KV store operations for instance state
type JetStreamManager struct {
	js        nats.JetStreamContext
	kv        nats.KeyValue // hive-instance-state
	clusterKV nats.KeyValue // hive-cluster-state
	replicas  int
	kvMu      sync.Mutex // protects kv during recovery
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
			slog.Debug("Creating JetStream KV bucket", "bucket", InstanceStateBucket, "replicas", m.replicas)
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
		slog.Debug("Connected to existing JetStream KV bucket", "bucket", InstanceStateBucket)
	}

	m.kv = kv
	return nil
}

// InitClusterStateBucket initializes the cluster-state KV bucket, creating it if it doesn't exist.
func (m *JetStreamManager) InitClusterStateBucket() error {
	kv, err := m.js.KeyValue(ClusterStateBucket)
	if err != nil {
		if errors.Is(err, nats.ErrBucketNotFound) {
			slog.Debug("Creating JetStream KV bucket", "bucket", ClusterStateBucket, "replicas", m.replicas)
			kv, err = m.js.CreateKeyValue(&nats.KeyValueConfig{
				Bucket:      ClusterStateBucket,
				Description: "Hive cluster state (heartbeats, shutdown markers, service maps)",
				History:     1,
				Replicas:    m.replicas,
				TTL:         1 * time.Hour,
			})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		slog.Debug("Connected to existing JetStream KV bucket", "bucket", ClusterStateBucket)
	}

	m.clusterKV = kv
	return nil
}

// isStreamUnavailable checks if an error indicates the underlying JetStream stream
// was lost or is unreachable. This can happen during NATS cluster formation when
// streams created with low replication are disrupted by node join/catchup operations.
// Different KV operations surface different errors when the stream is gone:
//   - Get/Keys → ErrNoResponders ("no responders available for request")
//   - Put/Delete → ErrNoStreamResponse ("no response from stream")
//   - Direct stream queries → ErrStreamNotFound ("stream not found")
func isStreamUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, nats.ErrStreamNotFound) ||
		errors.Is(err, nats.ErrNoStreamResponse) ||
		errors.Is(err, nats.ErrNoResponders) {
		return true
	}
	return strings.Contains(err.Error(), "stream not found")
}

// recoverKVBucket attempts to reconnect to or re-create the instance-state KV bucket
// after the underlying JetStream stream was lost during cluster formation.
func (m *JetStreamManager) recoverKVBucket() error {
	m.kvMu.Lock()
	defer m.kvMu.Unlock()

	// Try to reconnect to existing bucket first (another goroutine may have recovered it)
	kv, err := m.js.KeyValue(InstanceStateBucket)
	if err == nil {
		m.kv = kv
		slog.Info("Reconnected to KV bucket", "bucket", InstanceStateBucket)
		return nil
	}

	if !errors.Is(err, nats.ErrBucketNotFound) && !isStreamUnavailable(err) {
		return err
	}

	// Bucket truly doesn't exist — recreate it
	slog.Warn("KV bucket stream lost, recreating", "bucket", InstanceStateBucket, "replicas", m.replicas)
	kv, err = m.js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:      InstanceStateBucket,
		Description: "Hive instance state storage",
		History:     1,
		Replicas:    m.replicas,
	})
	if err != nil {
		slog.Error("Failed to recreate KV bucket", "bucket", InstanceStateBucket, "err", err)
		return err
	}

	m.kv = kv
	slog.Info("KV bucket recreated successfully", "bucket", InstanceStateBucket)
	return nil
}

// Heartbeat represents a daemon's periodic health status published to cluster KV.
type Heartbeat struct {
	Node          string   `json:"node"`
	Epoch         uint64   `json:"epoch"`
	Timestamp     string   `json:"timestamp"`
	Services      []string `json:"services"`
	VMCount       int      `json:"vm_count"`
	AllocatedVCPU int      `json:"allocated_vcpu"`
	AvailableVCPU int      `json:"available_vcpu"`
	AllocatedMem  float64  `json:"allocated_mem_gb"`
	AvailableMem  float64  `json:"available_mem_gb"`
}

// WriteHeartbeat writes a heartbeat entry for the given node to the cluster-state KV.
func (m *JetStreamManager) WriteHeartbeat(h *Heartbeat) error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = m.clusterKV.Put("heartbeat."+h.Node, data)
	return err
}

// ReadHeartbeat reads the heartbeat entry for the given node from the cluster-state KV.
func (m *JetStreamManager) ReadHeartbeat(nodeID string) (*Heartbeat, error) {
	if m.clusterKV == nil {
		return nil, errors.New("cluster state KV not initialized")
	}
	entry, err := m.clusterKV.Get("heartbeat." + nodeID)
	if err != nil {
		return nil, err
	}
	var h Heartbeat
	if err := json.Unmarshal(entry.Value(), &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// ClusterShutdownState tracks the coordinated cluster shutdown progress in KV.
type ClusterShutdownState struct {
	Initiator  string            `json:"initiator"`
	Phase      string            `json:"phase"`
	Started    string            `json:"started"`
	Timeout    string            `json:"timeout"`
	Force      bool              `json:"force"`
	NodesTotal int               `json:"nodes_total"`
	NodesAcked map[string]string `json:"nodes_acked"`
}

// WriteClusterShutdown writes the cluster shutdown state to KV.
func (m *JetStreamManager) WriteClusterShutdown(state *ClusterShutdownState) error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = m.clusterKV.Put("cluster.shutdown", data)
	return err
}

// ReadClusterShutdown reads the cluster shutdown state from KV.
func (m *JetStreamManager) ReadClusterShutdown() (*ClusterShutdownState, error) {
	if m.clusterKV == nil {
		return nil, errors.New("cluster state KV not initialized")
	}
	entry, err := m.clusterKV.Get("cluster.shutdown")
	if err != nil {
		return nil, err
	}
	var state ClusterShutdownState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// DeleteClusterShutdown removes the cluster shutdown state from KV.
func (m *JetStreamManager) DeleteClusterShutdown() error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	err := m.clusterKV.Delete("cluster.shutdown")
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}
	return nil
}

// WriteShutdownMarker writes a shutdown marker for the given node to the cluster-state KV.
func (m *JetStreamManager) WriteShutdownMarker(nodeID string) error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	data, _ := json.Marshal(map[string]any{
		"node":      nodeID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	_, err := m.clusterKV.Put("shutdown."+nodeID, data)
	return err
}

// ReadShutdownMarker checks if a clean shutdown marker exists for the given node.
func (m *JetStreamManager) ReadShutdownMarker(nodeID string) (bool, error) {
	if m.clusterKV == nil {
		return false, errors.New("cluster state KV not initialized")
	}
	_, err := m.clusterKV.Get("shutdown." + nodeID)
	if errors.Is(err, nats.ErrKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// DeleteShutdownMarker removes the shutdown marker for the given node.
func (m *JetStreamManager) DeleteShutdownMarker(nodeID string) error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	err := m.clusterKV.Delete("shutdown." + nodeID)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}
	return nil
}

// WriteServiceManifest writes the service manifest for the given node to the cluster-state KV.
func (m *JetStreamManager) WriteServiceManifest(nodeID string, services []string, natsHost, predastoreHost string) error {
	if m.clusterKV == nil {
		return errors.New("cluster state KV not initialized")
	}
	data, _ := json.Marshal(map[string]any{
		"node":            nodeID,
		"services":        services,
		"nats_host":       natsHost,
		"predastore_host": predastoreHost,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	})
	_, err := m.clusterKV.Put("node."+nodeID+".services", data)
	return err
}

// WriteState writes the instance state to the KV store for the given node.
// It acquires instances.Mu internally.
func (m *JetStreamManager) WriteState(nodeID string, instances *vm.Instances) error {
	instances.Mu.Lock()
	defer instances.Mu.Unlock()

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
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return err
			}
			if _, retryErr := m.kv.Put(key, jsonData); retryErr != nil {
				return retryErr
			}
			slog.Debug("Wrote state to JetStream KV (after recovery)", "key", key, "instances", len(instances.VMS))
			return nil
		}
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
			slog.Debug("No existing state in JetStream KV, returning empty state", "key", key)
			return &vm.Instances{VMS: make(map[string]*vm.VM)}, nil
		}
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return nil, err
			}
			// After recovery the bucket is empty, so no state exists
			slog.Debug("No existing state in JetStream KV (after recovery), returning empty state", "key", key)
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
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return err
			}
			// After recovery the bucket is empty, nothing to delete
			return nil
		}
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

	// Also update the cluster-state bucket if it exists
	if m.clusterKV != nil {
		clusterStreamName := "KV_" + ClusterStateBucket
		clusterInfo, err := m.js.StreamInfo(clusterStreamName)
		if err == nil && clusterInfo.Config.Replicas < newReplicas {
			clusterInfo.Config.Replicas = newReplicas
			if _, err := m.js.UpdateStream(&clusterInfo.Config); err != nil {
				slog.Warn("Failed to update cluster-state bucket replicas", "error", err)
			} else {
				slog.Info("Updated cluster-state bucket replicas", "bucket", ClusterStateBucket, "newReplicas", newReplicas)
			}
		}
	}

	return nil
}

// WriteStoppedInstance writes a stopped instance to the shared KV store.
func (m *JetStreamManager) WriteStoppedInstance(instanceID string, instance *vm.VM) error {
	if m.kv == nil {
		return errors.New("KV bucket not initialized")
	}

	jsonData, err := json.Marshal(instance)
	if err != nil {
		return err
	}

	key := StoppedInstancePrefix + instanceID
	_, err = m.kv.Put(key, jsonData)
	if err != nil {
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return err
			}
			if _, retryErr := m.kv.Put(key, jsonData); retryErr != nil {
				return retryErr
			}
			slog.Debug("Wrote stopped instance to JetStream KV (after recovery)", "key", key, "instanceId", instanceID)
			return nil
		}
		return err
	}

	slog.Debug("Wrote stopped instance to JetStream KV", "key", key, "instanceId", instanceID)
	return nil
}

// LoadStoppedInstance loads a stopped instance from the shared KV store.
// Returns nil, nil if the key does not exist.
func (m *JetStreamManager) LoadStoppedInstance(instanceID string) (*vm.VM, error) {
	if m.kv == nil {
		return nil, errors.New("KV bucket not initialized")
	}

	key := StoppedInstancePrefix + instanceID
	entry, err := m.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return nil, err
			}
			// After recovery the bucket is empty, instance doesn't exist
			return nil, nil
		}
		return nil, err
	}

	var instance vm.VM
	if err := json.Unmarshal(entry.Value(), &instance); err != nil {
		return nil, err
	}

	return &instance, nil
}

// DeleteStoppedInstance removes a stopped instance from the shared KV store.
// It is idempotent — deleting a non-existent key is not an error.
func (m *JetStreamManager) DeleteStoppedInstance(instanceID string) error {
	if m.kv == nil {
		return errors.New("KV bucket not initialized")
	}

	key := StoppedInstancePrefix + instanceID
	err := m.kv.Delete(key)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return err
			}
			// After recovery the bucket is empty, nothing to delete
			return nil
		}
		return err
	}

	slog.Debug("Deleted stopped instance from JetStream KV", "key", key)
	return nil
}

// ListStoppedInstances returns all stopped instances from the shared KV store.
func (m *JetStreamManager) ListStoppedInstances() ([]*vm.VM, error) {
	if m.kv == nil {
		return nil, errors.New("KV bucket not initialized")
	}

	keys, err := m.kv.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return nil, nil
		}
		if isStreamUnavailable(err) {
			if recoverErr := m.recoverKVBucket(); recoverErr != nil {
				return nil, err
			}
			// After recovery the bucket is empty, no stopped instances
			return nil, nil
		}
		return nil, err
	}

	var instances []*vm.VM
	for _, key := range keys {
		if !strings.HasPrefix(key, StoppedInstancePrefix) {
			continue
		}

		entry, err := m.kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				continue
			}
			return nil, err
		}

		var instance vm.VM
		if err := json.Unmarshal(entry.Value(), &instance); err != nil {
			slog.Error("Failed to unmarshal stopped instance", "key", key, "err", err)
			continue
		}

		instances = append(instances, &instance)
	}

	return instances, nil
}
