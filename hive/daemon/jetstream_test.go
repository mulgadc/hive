package daemon

import (
	"testing"

	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJetStreamManager_WriteAndLoadState tests round-trip write and load of instance state
func TestJetStreamManager_WriteAndLoadState(t *testing.T) {
	natsURL := sharedJSNATSURL

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	require.NoError(t, err, "Failed to connect to NATS")
	defer nc.Close()

	// Create JetStreamManager
	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err, "Failed to create JetStreamManager")

	// Initialize the KV bucket
	err = jsm.InitKVBucket()
	require.NoError(t, err, "Failed to init KV bucket")

	// Create test instances
	testNodeID := "test-node-1"
	testInstances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-test-001": {
				ID:           "i-test-001",
				Status:       vm.StateRunning,
				InstanceType: "t3.micro",
			},
			"i-test-002": {
				ID:           "i-test-002",
				Status:       vm.StateStopped,
				InstanceType: "t3.small",
			},
		},
	}

	// Write state
	err = jsm.WriteState(testNodeID, testInstances)
	require.NoError(t, err, "Failed to write state")

	// Load state
	loadedInstances, err := jsm.LoadState(testNodeID)
	require.NoError(t, err, "Failed to load state")
	require.NotNil(t, loadedInstances, "Loaded instances should not be nil")

	// Verify the loaded state matches
	assert.Len(t, loadedInstances.VMS, 2, "Should have 2 instances")
	assert.NotNil(t, loadedInstances.VMS["i-test-001"], "Should have i-test-001")
	assert.NotNil(t, loadedInstances.VMS["i-test-002"], "Should have i-test-002")
	assert.Equal(t, vm.StateRunning, loadedInstances.VMS["i-test-001"].Status)
	assert.Equal(t, vm.StateStopped, loadedInstances.VMS["i-test-002"].Status)
	assert.Equal(t, "t3.micro", loadedInstances.VMS["i-test-001"].InstanceType)
	assert.Equal(t, "t3.small", loadedInstances.VMS["i-test-002"].InstanceType)
}

// TestJetStreamManager_LoadState_KeyNotFound tests that LoadState returns empty state when key doesn't exist
func TestJetStreamManager_LoadState_KeyNotFound(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	// Load state for a non-existent node
	instances, err := jsm.LoadState("non-existent-node")
	require.NoError(t, err, "Should not error when key not found")
	require.NotNil(t, instances, "Should return non-nil instances")
	assert.Empty(t, instances.VMS, "Should return empty VMS map")
}

// TestJetStreamManager_BucketCreation tests that InitKVBucket creates the bucket when it doesn't exist
func TestJetStreamManager_BucketCreation(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	// InitKVBucket should create the bucket
	err = jsm.InitKVBucket()
	require.NoError(t, err, "Should create bucket without error")

	// Verify the bucket exists by checking jsm.kv is set
	assert.NotNil(t, jsm.kv, "KV bucket should be initialized")
}

// TestJetStreamManager_BucketReconnection tests that InitKVBucket connects to existing bucket
func TestJetStreamManager_BucketReconnection(t *testing.T) {
	natsURL := sharedJSNATSURL

	// First connection - create the bucket
	nc1, err := nats.Connect(natsURL)
	require.NoError(t, err)

	jsm1, err := NewJetStreamManager(nc1, 1)
	require.NoError(t, err)

	err = jsm1.InitKVBucket()
	require.NoError(t, err, "First InitKVBucket should succeed")

	// Write some test data
	testInstances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-persist": {
				ID:     "i-persist",
				Status: vm.StateRunning,
			},
		},
	}
	err = jsm1.WriteState("persist-node", testInstances)
	require.NoError(t, err)

	nc1.Close()

	// Second connection - should connect to existing bucket
	nc2, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc2.Close()

	jsm2, err := NewJetStreamManager(nc2, 1)
	require.NoError(t, err)

	err = jsm2.InitKVBucket()
	require.NoError(t, err, "Second InitKVBucket should succeed (reconnect)")

	// Verify data persisted
	loadedInstances, err := jsm2.LoadState("persist-node")
	require.NoError(t, err)
	assert.NotEmpty(t, loadedInstances.VMS, "Should have persisted instances")
	assert.NotNil(t, loadedInstances.VMS["i-persist"], "Should have i-persist")
	assert.Equal(t, vm.StateRunning, loadedInstances.VMS["i-persist"].Status)
}

// TestJetStreamManager_DeleteState tests deleting state from the KV store
func TestJetStreamManager_DeleteState(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	// Write state
	testNodeID := "delete-test-node"
	testInstances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-delete-me": {
				ID:     "i-delete-me",
				Status: vm.StateRunning,
			},
		},
	}
	err = jsm.WriteState(testNodeID, testInstances)
	require.NoError(t, err)

	// Verify state exists
	loadedInstances, err := jsm.LoadState(testNodeID)
	require.NoError(t, err)
	assert.NotEmpty(t, loadedInstances.VMS)

	// Delete state
	err = jsm.DeleteState(testNodeID)
	require.NoError(t, err, "Should delete state without error")

	// Verify state is gone (should return empty state)
	loadedInstances, err = jsm.LoadState(testNodeID)
	require.NoError(t, err)
	assert.Empty(t, loadedInstances.VMS, "Should return empty state after deletion")
}

// TestJetStreamManager_DeleteState_NonExistent tests deleting state that doesn't exist
func TestJetStreamManager_DeleteState_NonExistent(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	// Delete non-existent state should not error
	err = jsm.DeleteState("non-existent-node")
	require.NoError(t, err, "Deleting non-existent state should not error")
}

// TestJetStreamManager_WriteState_UpdateExisting tests that writing state updates existing entry
func TestJetStreamManager_WriteState_UpdateExisting(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	testNodeID := "update-test-node"

	// Write initial state
	initialInstances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-initial": {
				ID:     "i-initial",
				Status: vm.StateRunning,
			},
		},
	}
	err = jsm.WriteState(testNodeID, initialInstances)
	require.NoError(t, err)

	// Update state with different instances
	updatedInstances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-initial": {
				ID:     "i-initial",
				Status: vm.StateStopped, // Changed status
			},
			"i-new": { // Added new instance
				ID:     "i-new",
				Status: vm.StateRunning,
			},
		},
	}
	err = jsm.WriteState(testNodeID, updatedInstances)
	require.NoError(t, err)

	// Load and verify updated state
	loadedInstances, err := jsm.LoadState(testNodeID)
	require.NoError(t, err)
	assert.Len(t, loadedInstances.VMS, 2, "Should have 2 instances")
	assert.Equal(t, vm.StateStopped, loadedInstances.VMS["i-initial"].Status, "Status should be updated")
	assert.NotNil(t, loadedInstances.VMS["i-new"], "Should have new instance")
}

// TestJetStreamManager_MultipleNodes tests storing state for multiple nodes
func TestJetStreamManager_MultipleNodes(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	// Write state for node-1
	node1Instances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-node1-001": {ID: "i-node1-001", Status: vm.StateRunning},
		},
	}
	err = jsm.WriteState("node-1", node1Instances)
	require.NoError(t, err)

	// Write state for node-2
	node2Instances := &vm.Instances{
		VMS: map[string]*vm.VM{
			"i-node2-001": {ID: "i-node2-001", Status: vm.StateStopped},
			"i-node2-002": {ID: "i-node2-002", Status: vm.StateRunning},
		},
	}
	err = jsm.WriteState("node-2", node2Instances)
	require.NoError(t, err)

	// Load and verify node-1 state
	loadedNode1, err := jsm.LoadState("node-1")
	require.NoError(t, err)
	assert.Len(t, loadedNode1.VMS, 1)
	assert.NotNil(t, loadedNode1.VMS["i-node1-001"])

	// Load and verify node-2 state
	loadedNode2, err := jsm.LoadState("node-2")
	require.NoError(t, err)
	assert.Len(t, loadedNode2.VMS, 2)
	assert.NotNil(t, loadedNode2.VMS["i-node2-001"])
	assert.NotNil(t, loadedNode2.VMS["i-node2-002"])

	// Verify node isolation - node-1 doesn't have node-2's instances
	_, exists := loadedNode1.VMS["i-node2-001"]
	assert.False(t, exists, "Node-1 should not have node-2's instances")
}

// TestJetStreamManager_KVNotInitialized tests error handling when KV is not initialized
func TestJetStreamManager_KVNotInitialized(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	// Create JetStreamManager but don't call InitKVBucket
	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	testInstances := &vm.Instances{VMS: make(map[string]*vm.VM)}
	err = jsm.WriteState("test-node", testInstances)
	assert.Error(t, err, "WriteState should error when KV not initialized")

	_, err = jsm.LoadState("test-node")
	assert.Error(t, err, "LoadState should error when KV not initialized")

	err = jsm.DeleteState("test-node")
	assert.Error(t, err, "DeleteState should error when KV not initialized")
}

// TestJetStreamManager_UpdateReplicas tests updating replica count for the KV bucket
func TestJetStreamManager_UpdateReplicas(t *testing.T) {
	natsURL := sharedJSNATSURL

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	// Create with 1 replica (typical for single node startup)
	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)

	err = jsm.InitKVBucket()
	require.NoError(t, err)

	// Verify initial replica count
	js, _ := nc.JetStream()
	streamInfo, err := js.StreamInfo("KV_" + InstanceStateBucket)
	require.NoError(t, err)
	assert.Equal(t, 1, streamInfo.Config.Replicas, "Should start with 1 replica")

	// Try to update to same replica count (should be a no-op)
	err = jsm.UpdateReplicas(1)
	assert.NoError(t, err, "Updating to same replica count should succeed")

	// Note: Increasing replicas beyond 1 requires additional NATS servers in the cluster,
	// which we don't have in the test environment. In a single-node test server,
	// attempting to increase replicas will fail with "insufficient resources" error.
	// This test verifies the basic functionality works.
}

// TestJetStreamManager_UpdateReplicas_NoInit tests UpdateReplicas when JS not initialized
func TestJetStreamManager_UpdateReplicas_NoInit(t *testing.T) {
	// Test with nil JetStream context
	jsm := &JetStreamManager{
		js:       nil,
		replicas: 1,
	}

	err := jsm.UpdateReplicas(3)
	assert.Error(t, err, "UpdateReplicas should error when JetStream not initialized")
}
