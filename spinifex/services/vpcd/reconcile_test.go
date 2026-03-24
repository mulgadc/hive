package vpcd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcile_NoBootstrap(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn)

	result := Reconcile(context.Background(), topo, nil)
	assert.Equal(t, 0, result.RoutersCreated)
	assert.Equal(t, 0, result.SwitchesCreated)
	assert.Equal(t, 0, result.IGWsCreated)
}

func TestReconcile_EmptyBootstrap(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn)

	result := Reconcile(context.Background(), topo, &BootstrapVPC{})
	assert.Equal(t, 0, result.RoutersCreated)
}

func TestReconcile_CreatesBootstrapTopology(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name:       "wan",
			RangeStart: "192.168.1.200",
			RangeEnd:   "192.168.1.250",
			Gateway:    "192.168.1.1",
			GatewayIP:  "192.168.1.200",
			PrefixLen:  23,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	bootstrap := &BootstrapVPC{
		AccountID:  "000000000001",
		VpcId:      "vpc-test123",
		SubnetId:   "subnet-test456",
		Cidr:       "172.31.0.0/16",
		SubnetCidr: "172.31.0.0/20",
	}

	result := Reconcile(context.Background(), topo, bootstrap)
	assert.Equal(t, 1, result.RoutersCreated)
	assert.Equal(t, 1, result.SwitchesCreated)
	assert.Equal(t, 1, result.IGWsCreated)

	// Verify OVN objects exist
	ctx := context.Background()

	_, err := ovn.GetLogicalRouter(ctx, "vpc-vpc-test123")
	require.NoError(t, err)

	_, err = ovn.GetLogicalSwitch(ctx, "subnet-subnet-test456")
	require.NoError(t, err)

	_, err = ovn.GetLogicalSwitch(ctx, "ext-vpc-test123")
	require.NoError(t, err)

	// Router port for subnet
	_, err = ovn.GetLogicalRouterPort(ctx, "rtr-subnet-test456")
	require.NoError(t, err)

	// Gateway router port
	_, err = ovn.GetLogicalRouterPort(ctx, "gw-vpc-test123")
	require.NoError(t, err)

	// DHCP options should exist
	_, err = ovn.FindDHCPOptionsByExternalID(ctx, "spinifex:subnet_id", "subnet-test456")
	require.NoError(t, err)
}

func TestReconcile_Idempotent(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name:      "wan",
			Gateway:   "192.168.1.1",
			GatewayIP: "192.168.1.200",
			PrefixLen: 23,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	bootstrap := &BootstrapVPC{
		AccountID:  "000000000001",
		VpcId:      "vpc-idem",
		SubnetId:   "subnet-idem",
		Cidr:       "172.31.0.0/16",
		SubnetCidr: "172.31.0.0/20",
	}

	// First run creates everything
	r1 := Reconcile(context.Background(), topo, bootstrap)
	assert.Equal(t, 1, r1.RoutersCreated)
	assert.Equal(t, 1, r1.SwitchesCreated)
	assert.Equal(t, 1, r1.IGWsCreated)

	// Second run should skip everything (already exists)
	r2 := Reconcile(context.Background(), topo, bootstrap)
	assert.Equal(t, 0, r2.RoutersCreated)
	assert.Equal(t, 0, r2.SwitchesCreated)
	assert.Equal(t, 0, r2.IGWsCreated)
}

func TestReconcile_PartialState(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name:      "wan",
			Gateway:   "192.168.1.1",
			GatewayIP: "192.168.1.200",
			PrefixLen: 23,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	bootstrap := &BootstrapVPC{
		AccountID:  "000000000001",
		VpcId:      "vpc-partial",
		SubnetId:   "subnet-partial",
		Cidr:       "172.31.0.0/16",
		SubnetCidr: "172.31.0.0/20",
	}

	// Pre-create just the router (simulating partial OVN state)
	ctx := context.Background()
	_ = topo.reconcileVPC(ctx, "vpc-partial", "172.31.0.0/16")
	// IGW ID not needed for pre-creating just the router

	// Reconcile should skip router but create subnet + IGW
	result := Reconcile(ctx, topo, bootstrap)
	assert.Equal(t, 0, result.RoutersCreated)
	assert.Equal(t, 1, result.SwitchesCreated)
	assert.Equal(t, 1, result.IGWsCreated)
}
