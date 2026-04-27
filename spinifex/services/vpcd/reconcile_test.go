package vpcd

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	handlers_ec2_igw "github.com/mulgadc/spinifex/spinifex/handlers/ec2/igw"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/services/vpcd/nbdb"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
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

// --- ReconcileFromKV tests ---

// seedKVBuckets creates NATS KV buckets and populates them with test data.
func seedKVBuckets(t *testing.T, nc *nats.Conn, vpcs []handlers_ec2_vpc.VPCRecord, subnets []handlers_ec2_vpc.SubnetRecord, igws []handlers_ec2_igw.IGWRecord, enis []handlers_ec2_vpc.ENIRecord) {
	t.Helper()
	js, err := nc.JetStream()
	require.NoError(t, err)

	vpcKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketVPCs, History: 1})
	require.NoError(t, err)
	for _, v := range vpcs {
		data, _ := json.Marshal(v)
		_, err := vpcKV.Put(utils.AccountKey("000000000001", v.VpcId), data)
		require.NoError(t, err)
	}

	subnetKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketSubnets, History: 1})
	require.NoError(t, err)
	for _, s := range subnets {
		data, _ := json.Marshal(s)
		_, err := subnetKV.Put(utils.AccountKey("000000000001", s.SubnetId), data)
		require.NoError(t, err)
	}

	igwKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_igw.KVBucketIGW, History: 1})
	require.NoError(t, err)
	for _, i := range igws {
		data, _ := json.Marshal(i)
		_, err := igwKV.Put(utils.AccountKey("000000000001", i.InternetGatewayId), data)
		require.NoError(t, err)
	}

	eniKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketENIs, History: 1})
	require.NoError(t, err)
	for _, e := range enis {
		data, _ := json.Marshal(e)
		_, err := eniKV.Put(utils.AccountKey("000000000001", e.NetworkInterfaceId), data)
		require.NoError(t, err)
	}
}

func TestReconcileFromKV_CreatesFullTopology(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name:       "wan",
			RangeStart: "10.0.0.200",
			RangeEnd:   "10.0.0.250",
			Gateway:    "10.0.0.1",
			GatewayIP:  "10.0.0.200",
			PrefixLen:  24,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	seedKVBuckets(t, nc,
		[]handlers_ec2_vpc.VPCRecord{{
			VpcId: "vpc-kv1", CidrBlock: "10.100.0.0/16", State: "available",
			CreatedAt: time.Now(),
		}},
		[]handlers_ec2_vpc.SubnetRecord{{
			SubnetId: "subnet-kv1", VpcId: "vpc-kv1", CidrBlock: "10.100.1.0/24",
			State: "available", CreatedAt: time.Now(),
		}},
		[]handlers_ec2_igw.IGWRecord{{
			InternetGatewayId: "igw-kv1", VpcId: "vpc-kv1", State: "attached",
			CreatedAt: time.Now(),
		}},
		[]handlers_ec2_vpc.ENIRecord{{
			NetworkInterfaceId: "eni-kv1", SubnetId: "subnet-kv1", VpcId: "vpc-kv1",
			PrivateIpAddress: "10.100.1.10", MacAddress: "02:00:00:aa:bb:cc",
			Status: "in-use", CreatedAt: time.Now(),
		}},
	)

	result := ReconcileFromKV(ctx, nc, topo, nil)
	assert.Equal(t, 1, result.RoutersCreated)
	assert.Equal(t, 1, result.SwitchesCreated)
	assert.Equal(t, 1, result.IGWsCreated)
	assert.Equal(t, 1, result.PortsCreated)

	// Verify OVN objects
	_, err := ovn.GetLogicalRouter(ctx, "vpc-vpc-kv1")
	require.NoError(t, err)

	_, err = ovn.GetLogicalSwitch(ctx, "subnet-subnet-kv1")
	require.NoError(t, err)

	_, err = ovn.GetLogicalSwitch(ctx, "ext-vpc-kv1")
	require.NoError(t, err)

	lsp, err := ovn.GetLogicalSwitchPort(ctx, "port-eni-kv1")
	require.NoError(t, err)
	assert.Contains(t, lsp.Addresses[0], "10.100.1.10")
}

func TestReconcileFromKV_Idempotent(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name: "wan", Gateway: "10.0.0.1", GatewayIP: "10.0.0.200", PrefixLen: 24,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	seedKVBuckets(t, nc,
		[]handlers_ec2_vpc.VPCRecord{{
			VpcId: "vpc-idem2", CidrBlock: "10.200.0.0/16", State: "available",
			CreatedAt: time.Now(),
		}},
		[]handlers_ec2_vpc.SubnetRecord{{
			SubnetId: "subnet-idem2", VpcId: "vpc-idem2", CidrBlock: "10.200.1.0/24",
			State: "available", CreatedAt: time.Now(),
		}},
		nil, nil,
	)

	r1 := ReconcileFromKV(ctx, nc, topo, nil)
	assert.Equal(t, 1, r1.RoutersCreated)
	assert.Equal(t, 1, r1.SwitchesCreated)

	// Second run: everything exists
	r2 := ReconcileFromKV(ctx, nc, topo, nil)
	assert.Equal(t, 0, r2.RoutersCreated)
	assert.Equal(t, 0, r2.SwitchesCreated)
}

func TestReconcileFromKV_SkipsDetachedIGW(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name: "wan", Gateway: "10.0.0.1", GatewayIP: "10.0.0.200", PrefixLen: 24,
		}}),
	)

	seedKVBuckets(t, nc,
		[]handlers_ec2_vpc.VPCRecord{{
			VpcId: "vpc-det", CidrBlock: "10.50.0.0/16", State: "available",
			CreatedAt: time.Now(),
		}},
		nil,
		[]handlers_ec2_igw.IGWRecord{{
			InternetGatewayId: "igw-det", VpcId: "", State: "available",
			CreatedAt: time.Now(),
		}},
		nil,
	)

	result := ReconcileFromKV(ctx, nc, topo, nil)
	assert.Equal(t, 1, result.RoutersCreated) // VPC router created
	assert.Equal(t, 0, result.IGWsCreated)    // Detached IGW skipped

	_, err := ovn.GetLogicalSwitch(ctx, "ext-vpc-det")
	assert.Error(t, err) // External switch should NOT exist
}

func TestReconcileFromKV_NoBuckets(t *testing.T) {
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())

	topo := NewTopologyHandler(ovn)

	// No KV buckets seeded — should handle gracefully
	result := ReconcileFromKV(context.Background(), nc, topo, nil)
	assert.Equal(t, 0, result.RoutersCreated)
	assert.Equal(t, 0, result.SwitchesCreated)
	assert.Equal(t, 0, result.IGWsCreated)
	assert.Equal(t, 0, result.PortsCreated)
}

func TestReconcileFromKV_VersionKeysAndBadJSON(t *testing.T) {
	// Tests that _version keys are skipped and malformed JSON records are handled gracefully.
	// This covers the "continue" branches for version key filtering and unmarshal errors.
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	ctx := context.Background()

	topo := NewTopologyHandler(ovn,
		WithExternalNetwork("pool", []ExternalPoolConfig{{
			Name:       "wan",
			RangeStart: "10.0.0.200",
			RangeEnd:   "10.0.0.250",
			Gateway:    "10.0.0.1",
			GatewayIP:  "10.0.0.200",
			PrefixLen:  24,
		}}),
		WithChassisNames([]string{"chassis-node1"}),
	)

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create VPC bucket with _version key and one bad JSON record
	vpcKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketVPCs, History: 1})
	require.NoError(t, err)
	_, err = vpcKV.PutString(utils.VersionKey, "1")
	require.NoError(t, err)
	_, err = vpcKV.Put("bad-vpc-record", []byte("not-json"))
	require.NoError(t, err)
	// Also add a valid VPC record
	vpcData, _ := json.Marshal(handlers_ec2_vpc.VPCRecord{
		VpcId: "vpc-ver1", CidrBlock: "10.100.0.0/16", State: "available",
		CreatedAt: time.Now(),
	})
	_, err = vpcKV.Put(utils.AccountKey("000000000001", "vpc-ver1"), vpcData)
	require.NoError(t, err)

	// Create subnet bucket with _version key and one bad JSON record
	subnetKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketSubnets, History: 1})
	require.NoError(t, err)
	_, err = subnetKV.PutString(utils.VersionKey, "1")
	require.NoError(t, err)
	_, err = subnetKV.Put("bad-subnet", []byte("{invalid"))
	require.NoError(t, err)
	subnetData, _ := json.Marshal(handlers_ec2_vpc.SubnetRecord{
		SubnetId: "subnet-ver1", VpcId: "vpc-ver1", CidrBlock: "10.100.1.0/24",
		State: "available", CreatedAt: time.Now(),
	})
	_, err = subnetKV.Put(utils.AccountKey("000000000001", "subnet-ver1"), subnetData)
	require.NoError(t, err)

	// Create IGW bucket with _version key and one bad JSON record
	igwKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_igw.KVBucketIGW, History: 1})
	require.NoError(t, err)
	_, err = igwKV.PutString(utils.VersionKey, "1")
	require.NoError(t, err)
	_, err = igwKV.Put("bad-igw", []byte("???"))
	require.NoError(t, err)
	igwData, _ := json.Marshal(handlers_ec2_igw.IGWRecord{
		InternetGatewayId: "igw-ver1", VpcId: "vpc-ver1", State: "attached",
		CreatedAt: time.Now(),
	})
	_, err = igwKV.Put(utils.AccountKey("000000000001", "igw-ver1"), igwData)
	require.NoError(t, err)

	// Create ENI bucket with _version key and one bad JSON record
	eniKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketENIs, History: 1})
	require.NoError(t, err)
	_, err = eniKV.PutString(utils.VersionKey, "1")
	require.NoError(t, err)
	_, err = eniKV.Put("bad-eni", []byte("nope"))
	require.NoError(t, err)
	eniData, _ := json.Marshal(handlers_ec2_vpc.ENIRecord{
		NetworkInterfaceId: "eni-ver1", SubnetId: "subnet-ver1", VpcId: "vpc-ver1",
		PrivateIpAddress: "10.100.1.10", MacAddress: "02:00:00:aa:bb:01",
		Status: "in-use", CreatedAt: time.Now(),
	})
	_, err = eniKV.Put(utils.AccountKey("000000000001", "eni-ver1"), eniData)
	require.NoError(t, err)

	result := ReconcileFromKV(ctx, nc, topo, nil)

	// Valid records should still be reconciled despite bad records
	assert.Equal(t, 1, result.RoutersCreated)
	assert.Equal(t, 1, result.SwitchesCreated)
	assert.Equal(t, 1, result.IGWsCreated)
	assert.Equal(t, 1, result.PortsCreated)

	// Verify OVN objects created from valid records
	_, err = ovn.GetLogicalRouter(ctx, "vpc-vpc-ver1")
	require.NoError(t, err)
	_, err = ovn.GetLogicalSwitch(ctx, "subnet-subnet-ver1")
	require.NoError(t, err)
	_, err = ovn.GetLogicalSwitch(ctx, "ext-vpc-ver1")
	require.NoError(t, err)
	_, err = ovn.GetLogicalSwitchPort(ctx, "port-eni-ver1")
	require.NoError(t, err)
}

func TestReconcileFromKV_EmptyBuckets(t *testing.T) {
	// Tests the nats.ErrNoKeysFound branch when KV buckets exist but are empty.
	_, nc := startTestJetStreamNATS(t)
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())

	topo := NewTopologyHandler(ovn)

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create all KV buckets but leave them empty (no keys at all)
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketVPCs, History: 1})
	require.NoError(t, err)
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketSubnets, History: 1})
	require.NoError(t, err)
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_igw.KVBucketIGW, History: 1})
	require.NoError(t, err)
	_, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketENIs, History: 1})
	require.NoError(t, err)

	result := ReconcileFromKV(context.Background(), nc, topo, nil)
	assert.Equal(t, 0, result.RoutersCreated)
	assert.Equal(t, 0, result.SwitchesCreated)
	assert.Equal(t, 0, result.IGWsCreated)
	assert.Equal(t, 0, result.PortsCreated)
}

// --- reconcileGatewayChassis tests (mulga-999) ---

// seedGatewayLRP creates a router + LRP tagged spinifex:role=gateway so
// reconcileGatewayChassis's rebind step has something to bind.
func seedGatewayLRP(t *testing.T, ovn *MockOVNClient, routerName, lrpName string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, ovn.CreateLogicalRouter(ctx, &nbdb.LogicalRouter{Name: routerName}))
	require.NoError(t, ovn.CreateLogicalRouterPort(ctx, routerName, &nbdb.LogicalRouterPort{
		Name:        lrpName,
		ExternalIDs: map[string]string{"spinifex:role": "gateway"},
	}))
}

func TestReconcileGatewayChassis_RemovesStaleRows(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn)
	ctx := context.Background()

	seedGatewayLRP(t, ovn, "vpc-A", "gw-A")
	// One stale (chassis-old not in valid set), one live (UUID-A is).
	ovn.SeedGatewayChassis("gw-A", &nbdb.GatewayChassis{
		Name:        "gw-A-chassis-old",
		ChassisName: "chassis-old",
		Priority:    20,
	})
	ovn.SeedGatewayChassis("gw-A", &nbdb.GatewayChassis{
		Name:        "gw-A-UUID-A",
		ChassisName: "UUID-A",
		Priority:    20,
	})

	require.NoError(t, topo.reconcileGatewayChassis(ctx, []string{"UUID-A"}))

	rows, err := ovn.ListGatewayChassis(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "stale row should be deleted, live row should remain")
	assert.Equal(t, "UUID-A", rows[0].ChassisName)
	assert.Equal(t, 1, ovn.DeleteGatewayChassisCalls)
}

func TestReconcileGatewayChassis_RebindsAllGatewayLRPs(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn)
	ctx := context.Background()

	seedGatewayLRP(t, ovn, "vpc-A", "gw-A")
	seedGatewayLRP(t, ovn, "vpc-B", "gw-B")
	// Untagged LRP must NOT be rebinded.
	require.NoError(t, ovn.CreateLogicalRouter(ctx, &nbdb.LogicalRouter{Name: "vpc-C"}))
	require.NoError(t, ovn.CreateLogicalRouterPort(ctx, "vpc-C", &nbdb.LogicalRouterPort{
		Name:        "rtr-subnet-C",
		ExternalIDs: map[string]string{"spinifex:role": "internal"},
	}))

	require.NoError(t, topo.reconcileGatewayChassis(ctx, []string{"chassis-A", "chassis-B"}))

	// 2 gateway LRPs × 2 chassis = 4 SetGatewayChassis create calls. The
	// internal LRP must contribute zero.
	assert.Equal(t, 4, ovn.SetGatewayChassisCalls)
	rows, err := ovn.ListGatewayChassis(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 4)

	// Verify priorities — first chassis gets 20, second 15.
	prioByGC := make(map[string]int, len(rows))
	for _, gc := range rows {
		prioByGC[gc.Name] = gc.Priority
	}
	assert.Equal(t, 20, prioByGC["gw-A-chassis-A"])
	assert.Equal(t, 15, prioByGC["gw-A-chassis-B"])
	assert.Equal(t, 20, prioByGC["gw-B-chassis-A"])
	assert.Equal(t, 15, prioByGC["gw-B-chassis-B"])
}

func TestReconcileGatewayChassis_NoOpWhenAlreadyCorrect(t *testing.T) {
	ovn := NewMockOVNClient()
	_ = ovn.Connect(context.Background())
	topo := NewTopologyHandler(ovn)
	ctx := context.Background()

	seedGatewayLRP(t, ovn, "vpc-A", "gw-A")
	require.NoError(t, topo.reconcileGatewayChassis(ctx, []string{"chassis-A"}))
	// Reset counters from the first (creating) pass.
	ovn.SetGatewayChassisCalls = 0
	ovn.DeleteGatewayChassisCalls = 0
	ovn.UpdateGatewayChassisPriorityCalls = 0

	// Second pass: state is already correct; idempotent path must take no
	// destructive or mutative action.
	require.NoError(t, topo.reconcileGatewayChassis(ctx, []string{"chassis-A"}))

	assert.Equal(t, 0, ovn.SetGatewayChassisCalls, "no new creates expected")
	assert.Equal(t, 0, ovn.DeleteGatewayChassisCalls, "no deletes expected")
	assert.Equal(t, 0, ovn.UpdateGatewayChassisPriorityCalls, "no priority updates expected")
}
