package daemon

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildHeartbeat verifies that buildHeartbeat populates the struct from daemon state.
func TestBuildHeartbeat(t *testing.T) {
	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		node: "test-node",
		clusterConfig: &config.ClusterConfig{
			Epoch: 5,
		},
		config: &config.Config{
			Services: []string{"daemon", "nats", "viperblock"},
		},
		resourceMgr: rm,
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
	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		node: "alloc-node",
		clusterConfig: &config.ClusterConfig{
			Epoch: 1,
		},
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: rm,
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
	err = d.resourceMgr.allocate(d.resourceMgr.instanceTypes[allocType])
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
