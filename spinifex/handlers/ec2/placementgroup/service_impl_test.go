package handlers_ec2_placementgroup

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccountID = "123456789012"

func setupTestService(t *testing.T) *PlacementGroupServiceImpl {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc, err := NewPlacementGroupServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

func createTestGroup(t *testing.T, svc *PlacementGroupServiceImpl, name, strategy string) *ec2.PlacementGroup {
	t.Helper()
	out, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String(name),
		Strategy:  aws.String(strategy),
	}, testAccountID)
	require.NoError(t, err)
	return out.PlacementGroup
}

// --- CreatePlacementGroup Tests ---

func TestCreatePlacementGroup_Spread(t *testing.T) {
	svc := setupTestService(t)
	pg := createTestGroup(t, svc, "my-spread-group", "spread")

	assert.Equal(t, "pg-", (*pg.GroupId)[:3])
	assert.Equal(t, "my-spread-group", *pg.GroupName)
	assert.Equal(t, "spread", *pg.Strategy)
	assert.Equal(t, "available", *pg.State)
	assert.Equal(t, "host", *pg.SpreadLevel)
}

func TestCreatePlacementGroup_Cluster(t *testing.T) {
	svc := setupTestService(t)
	pg := createTestGroup(t, svc, "my-cluster-group", "cluster")

	assert.Equal(t, "pg-", (*pg.GroupId)[:3])
	assert.Equal(t, "my-cluster-group", *pg.GroupName)
	assert.Equal(t, "cluster", *pg.Strategy)
	assert.Equal(t, "available", *pg.State)
	// SpreadLevel should be nil for cluster strategy
	assert.Nil(t, pg.SpreadLevel)
}

func TestCreatePlacementGroup_DuplicateName(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "dup-group", "spread")

	_, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String("dup-group"),
		Strategy:  aws.String("spread"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidPlacementGroupDuplicate, err.Error())
}

func TestCreatePlacementGroup_PartitionRejected(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String("part-group"),
		Strategy:  aws.String("partition"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreatePlacementGroup_InvalidStrategy(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String("bad-group"),
		Strategy:  aws.String("invalid"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreatePlacementGroup_MissingName(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		Strategy: aws.String("spread"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestCreatePlacementGroup_MissingStrategy(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String("no-strat"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestCreatePlacementGroup_SameNameDifferentAccounts(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "shared-name", "spread")

	// Different account should succeed
	out, err := svc.CreatePlacementGroup(&ec2.CreatePlacementGroupInput{
		GroupName: aws.String("shared-name"),
		Strategy:  aws.String("cluster"),
	}, "999999999999")
	require.NoError(t, err)
	assert.Equal(t, "shared-name", *out.PlacementGroup.GroupName)
}

// --- DeletePlacementGroup Tests ---

func TestDeletePlacementGroup_Success(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "del-group", "spread")

	_, err := svc.DeletePlacementGroup(&ec2.DeletePlacementGroupInput{
		GroupName: aws.String("del-group"),
	}, testAccountID)
	require.NoError(t, err)

	// Verify it's gone
	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, out.PlacementGroups)
}

func TestDeletePlacementGroup_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeletePlacementGroup(&ec2.DeletePlacementGroupInput{
		GroupName: aws.String("nonexistent"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidPlacementGroupUnknown, err.Error())
}

func TestDeletePlacementGroup_InUse(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "in-use-group", "spread")

	// Simulate instances by updating the record directly
	record, entry, err := svc.GetPlacementGroupRecord(testAccountID, "in-use-group")
	require.NoError(t, err)
	record.NodeInstances["node1"] = []string{"i-123"}
	err = svc.UpdatePlacementGroupRecord(testAccountID, "in-use-group", record, entry.Revision())
	require.NoError(t, err)

	_, err = svc.DeletePlacementGroup(&ec2.DeletePlacementGroupInput{
		GroupName: aws.String("in-use-group"),
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidPlacementGroupInUse, err.Error())
}

func TestDeletePlacementGroup_MissingName(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DeletePlacementGroup(&ec2.DeletePlacementGroupInput{}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

// --- DescribePlacementGroups Tests ---

func TestDescribePlacementGroups_All(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "group-a", "spread")
	createTestGroup(t, svc, "group-b", "cluster")

	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.PlacementGroups, 2)
}

func TestDescribePlacementGroups_FilterByName(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "alpha", "spread")
	createTestGroup(t, svc, "beta", "cluster")

	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		GroupNames: []*string{aws.String("alpha")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.PlacementGroups, 1)
	assert.Equal(t, "alpha", *out.PlacementGroups[0].GroupName)
}

func TestDescribePlacementGroups_FilterByGroupId(t *testing.T) {
	svc := setupTestService(t)
	pg := createTestGroup(t, svc, "id-filter", "spread")

	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		GroupIds: []*string{pg.GroupId},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.PlacementGroups, 1)
	assert.Equal(t, *pg.GroupId, *out.PlacementGroups[0].GroupId)
}

func TestDescribePlacementGroups_FilterByStrategy(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "spread1", "spread")
	createTestGroup(t, svc, "cluster1", "cluster")

	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("strategy"), Values: []*string{aws.String("spread")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.PlacementGroups, 1)
	assert.Equal(t, "spread1", *out.PlacementGroups[0].GroupName)
}

func TestDescribePlacementGroups_FilterByState(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "avail-group", "spread")

	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("state"), Values: []*string{aws.String("available")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.PlacementGroups, 1)

	out, err = svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("state"), Values: []*string{aws.String("deleting")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, out.PlacementGroups)
}

func TestDescribePlacementGroups_NameNotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{
		GroupNames: []*string{aws.String("ghost")},
	}, testAccountID)
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidPlacementGroupUnknown, err.Error())
}

func TestDescribePlacementGroups_AccountScoped(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "acct-group", "spread")

	// Different account should see nothing
	out, err := svc.DescribePlacementGroups(&ec2.DescribePlacementGroupsInput{}, "999999999999")
	require.NoError(t, err)
	assert.Empty(t, out.PlacementGroups)
}

// --- NodeInstances CAS Tests ---

func TestGetAndUpdatePlacementGroupRecord(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "cas-group", "spread")

	// Get with revision
	record, entry, err := svc.GetPlacementGroupRecord(testAccountID, "cas-group")
	require.NoError(t, err)
	assert.Equal(t, "spread", record.Strategy)
	assert.Empty(t, record.NodeInstances)

	// Update with CAS
	record.NodeInstances["node1"] = []string{"i-abc"}
	err = svc.UpdatePlacementGroupRecord(testAccountID, "cas-group", record, entry.Revision())
	require.NoError(t, err)

	// Verify update
	record2, _, err := svc.GetPlacementGroupRecord(testAccountID, "cas-group")
	require.NoError(t, err)
	assert.Equal(t, []string{"i-abc"}, record2.NodeInstances["node1"])
}

func TestUpdatePlacementGroupRecord_CASConflict(t *testing.T) {
	svc := setupTestService(t)
	createTestGroup(t, svc, "conflict-group", "spread")

	// Get the record twice (same revision)
	record1, entry1, err := svc.GetPlacementGroupRecord(testAccountID, "conflict-group")
	require.NoError(t, err)
	record2, _, err := svc.GetPlacementGroupRecord(testAccountID, "conflict-group")
	require.NoError(t, err)

	// First update succeeds
	record1.NodeInstances["node1"] = []string{"i-111"}
	err = svc.UpdatePlacementGroupRecord(testAccountID, "conflict-group", record1, entry1.Revision())
	require.NoError(t, err)

	// Second update with stale revision fails
	record2.NodeInstances["node2"] = []string{"i-222"}
	err = svc.UpdatePlacementGroupRecord(testAccountID, "conflict-group", record2, entry1.Revision())
	require.Error(t, err)
}

func TestGetPlacementGroupRecord_NotFound(t *testing.T) {
	svc := setupTestService(t)
	_, _, err := svc.GetPlacementGroupRecord(testAccountID, "nonexistent")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidPlacementGroupUnknown, err.Error())
}
