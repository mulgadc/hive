package daemon

import (
	"testing"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildHeartbeat verifies that buildHeartbeat populates the struct from daemon state.
func TestBuildHeartbeat(t *testing.T) {
	d := &Daemon{
		node: "test-node",
		clusterConfig: &config.ClusterConfig{
			Epoch: 5,
		},
		config: &config.Config{
			Services: []string{"daemon", "nats", "viperblock"},
		},
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}

	h := d.buildHeartbeat()

	assert.Equal(t, "test-node", h.Node)
	assert.Equal(t, uint64(5), h.Epoch)
	assert.NotEmpty(t, h.Timestamp)
	assert.Equal(t, []string{"daemon", "nats", "viperblock"}, h.Services)
	assert.Equal(t, 0, h.VMCount)
	assert.Equal(t, 0, h.AllocatedVCPU)
	assert.Greater(t, h.AvailableVCPU, 0)
	assert.Greater(t, h.AvailableMem, 0.0)
}

// TestHeartbeatReflectsAllocation verifies that allocating resources changes the heartbeat values.
func TestHeartbeatReflectsAllocation(t *testing.T) {
	d := &Daemon{
		node: "alloc-node",
		clusterConfig: &config.ClusterConfig{
			Epoch: 1,
		},
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}

	// Take a heartbeat before allocation
	before := d.buildHeartbeat()

	// Find an instance type we can allocate
	var allocType string
	for typeName := range d.resourceMgr.instanceTypes {
		if d.resourceMgr.canAllocate(d.resourceMgr.instanceTypes[typeName], 1) >= 1 {
			allocType = typeName
			break
		}
	}
	require.NotEmpty(t, allocType, "Should have at least one allocatable instance type")

	// Allocate resources
	err := d.resourceMgr.allocate(d.resourceMgr.instanceTypes[allocType])
	require.NoError(t, err)

	// Add a VM to the instance map
	d.Instances.Mu.Lock()
	d.Instances.VMS["i-test-001"] = &vm.VM{
		ID:           "i-test-001",
		Status:       vm.StateRunning,
		InstanceType: allocType,
	}
	d.Instances.Mu.Unlock()

	// Take a heartbeat after allocation
	after := d.buildHeartbeat()

	assert.Equal(t, 1, after.VMCount, "Should reflect 1 VM")
	assert.Greater(t, after.AllocatedVCPU, before.AllocatedVCPU, "AllocatedVCPU should increase")
	assert.Less(t, after.AvailableVCPU, before.AvailableVCPU, "AvailableVCPU should decrease")
}

// TestHeartbeatKVRoundTrip verifies that heartbeat can be written to and read from KV.
func TestHeartbeatKVRoundTrip(t *testing.T) {
	nc, err := nats.Connect(sharedJSNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)
	err = jsm.InitClusterStateBucket()
	require.NoError(t, err)

	h := &Heartbeat{
		Node:          "kv-test-node",
		Epoch:         3,
		Timestamp:     "2025-01-01T00:00:00Z",
		Services:      []string{"daemon", "nats"},
		VMCount:       2,
		AllocatedVCPU: 4,
		AvailableVCPU: 12,
		AllocatedMem:  8.0,
		AvailableMem:  24.0,
	}

	err = jsm.WriteHeartbeat(h)
	require.NoError(t, err)

	loaded, err := jsm.ReadHeartbeat("kv-test-node")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, h.Node, loaded.Node)
	assert.Equal(t, h.Epoch, loaded.Epoch)
	assert.Equal(t, h.Timestamp, loaded.Timestamp)
	assert.Equal(t, h.Services, loaded.Services)
	assert.Equal(t, h.VMCount, loaded.VMCount)
	assert.Equal(t, h.AllocatedVCPU, loaded.AllocatedVCPU)
	assert.Equal(t, h.AvailableVCPU, loaded.AvailableVCPU)
	assert.Equal(t, h.AllocatedMem, loaded.AllocatedMem)
	assert.Equal(t, h.AvailableMem, loaded.AvailableMem)
}
