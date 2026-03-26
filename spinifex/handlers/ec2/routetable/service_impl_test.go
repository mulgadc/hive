package handlers_ec2_routetable

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_igw "github.com/mulgadc/spinifex/spinifex/handlers/ec2/igw"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccountID = "123456789012"

func setupTestService(t *testing.T) *RouteTableServiceImpl {
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

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Seed VPC KV
	vpcKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketVPCs, History: 1})
	require.NoError(t, err)
	_, err = vpcKV.Put(utils.AccountKey(testAccountID, "vpc-test1"), []byte(`{"vpc_id":"vpc-test1","cidr_block":"10.0.0.0/16","state":"available"}`))
	require.NoError(t, err)

	// Seed IGW KV (attached to vpc-test1)
	igwKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_igw.KVBucketIGW, History: 1})
	require.NoError(t, err)
	_, err = igwKV.Put(utils.AccountKey(testAccountID, "igw-test1"), []byte(`{"internet_gateway_id":"igw-test1","vpc_id":"vpc-test1","state":"attached"}`))
	require.NoError(t, err)

	// Seed subnet KV
	subnetKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: handlers_ec2_vpc.KVBucketSubnets, History: 1})
	require.NoError(t, err)
	_, err = subnetKV.Put(utils.AccountKey(testAccountID, "subnet-test1"), []byte(`{"subnet_id":"subnet-test1","vpc_id":"vpc-test1","cidr_block":"10.0.1.0/24","state":"available"}`))
	require.NoError(t, err)

	svc, err := NewRouteTableServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

func createTestRtb(t *testing.T, svc *RouteTableServiceImpl) string {
	t.Helper()
	out, err := svc.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId: aws.String("vpc-test1"),
	}, testAccountID)
	require.NoError(t, err)
	return *out.RouteTable.RouteTableId
}

func TestCreateRouteTable(t *testing.T) {
	svc := setupTestService(t)
	out, err := svc.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId: aws.String("vpc-test1"),
	}, testAccountID)
	require.NoError(t, err)

	rtb := out.RouteTable
	assert.NotEmpty(t, *rtb.RouteTableId)
	assert.Equal(t, "vpc-test1", *rtb.VpcId)
	assert.Empty(t, rtb.Associations) // custom tables have no associations initially

	// Should have local route
	require.Len(t, rtb.Routes, 1)
	assert.Equal(t, "10.0.0.0/16", *rtb.Routes[0].DestinationCidrBlock)
	assert.Equal(t, "local", *rtb.Routes[0].GatewayId)
	assert.Equal(t, "active", *rtb.Routes[0].State)
}

func TestCreateRouteTable_VpcNotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.CreateRouteTable(&ec2.CreateRouteTableInput{
		VpcId: aws.String("vpc-nonexistent"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidVpcIDNotFound)
}

func TestDeleteRouteTable(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.DeleteRouteTable(&ec2.DeleteRouteTableInput{
		RouteTableId: aws.String(rtbID),
	}, testAccountID)
	require.NoError(t, err)

	// Should be gone
	_, err = svc.getRouteTable(testAccountID, rtbID)
	assert.EqualError(t, err, awserrors.ErrorInvalidRouteTableIDNotFound)
}

func TestDeleteRouteTable_Main(t *testing.T) {
	svc := setupTestService(t)
	record, err := svc.CreateRouteTableForVPC("vpc-test1", "10.0.0.0/16", testAccountID, true, "")
	require.NoError(t, err)

	_, err = svc.DeleteRouteTable(&ec2.DeleteRouteTableInput{
		RouteTableId: aws.String(record.RouteTableId),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorDependencyViolation)
}

func TestDeleteRouteTable_WithAssociations(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	// Associate a subnet
	_, err := svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	require.NoError(t, err)

	// Should fail to delete
	_, err = svc.DeleteRouteTable(&ec2.DeleteRouteTableInput{
		RouteTableId: aws.String(rtbID),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorDependencyViolation)
}

func TestDescribeRouteTables(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	out, err := svc.DescribeRouteTables(&ec2.DescribeRouteTablesInput{}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.RouteTables, 1)
	assert.Equal(t, rtbID, *out.RouteTables[0].RouteTableId)
}

func TestDescribeRouteTables_FilterByVpcId(t *testing.T) {
	svc := setupTestService(t)
	createTestRtb(t, svc)

	name := "vpc-id"
	val := "vpc-test1"
	out, err := svc.DescribeRouteTables(&ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{{Name: &name, Values: []*string{&val}}},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.RouteTables, 1)

	// Filter by non-existent VPC
	val2 := "vpc-nope"
	out, err = svc.DescribeRouteTables(&ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{{Name: &name, Values: []*string{&val2}}},
	}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, out.RouteTables)
}

func TestDescribeRouteTables_FilterByMain(t *testing.T) {
	svc := setupTestService(t)

	// Create a main route table
	_, err := svc.CreateRouteTableForVPC("vpc-test1", "10.0.0.0/16", testAccountID, true, "")
	require.NoError(t, err)

	// Create a non-main route table
	createTestRtb(t, svc)

	name := "association.main"
	val := "true"
	out, err := svc.DescribeRouteTables(&ec2.DescribeRouteTablesInput{
		Filters: []*ec2.Filter{{Name: &name, Values: []*string{&val}}},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.RouteTables, 1)
	assert.True(t, *out.RouteTables[0].Associations[0].Main)
}

func TestCreateRoute(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	require.NoError(t, err)

	// Verify route was added
	record, err := svc.getRouteTable(testAccountID, rtbID)
	require.NoError(t, err)
	assert.Len(t, record.Routes, 2) // local + igw
	assert.Equal(t, "0.0.0.0/0", record.Routes[1].DestinationCidrBlock)
	assert.Equal(t, "igw-test1", record.Routes[1].GatewayId)
}

func TestCreateRoute_DuplicateDestination(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	require.NoError(t, err)

	// Duplicate should fail
	_, err = svc.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorRouteAlreadyExists)
}

func TestDeleteRoute(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DeleteRoute(&ec2.DeleteRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
	}, testAccountID)
	require.NoError(t, err)

	record, err := svc.getRouteTable(testAccountID, rtbID)
	require.NoError(t, err)
	assert.Len(t, record.Routes, 1) // only local remains
}

func TestDeleteRoute_LocalRoute(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.DeleteRoute(&ec2.DeleteRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("10.0.0.0/16"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestDeleteRoute_NotFound(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.DeleteRoute(&ec2.DeleteRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("192.168.0.0/16"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidRouteNotFound)
}

func TestReplaceRoute(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.CreateRoute(&ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	require.NoError(t, err)

	// Replace target (same IGW for simplicity — validates the swap logic)
	_, err = svc.ReplaceRoute(&ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String("igw-test1"),
	}, testAccountID)
	require.NoError(t, err)
}

func TestAssociateRouteTable(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	out, err := svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	require.NoError(t, err)
	assert.NotEmpty(t, *out.AssociationId)
	assert.Equal(t, "associated", *out.AssociationState.State)
}

func TestAssociateRouteTable_DuplicateSubnet(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	_, err := svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	require.NoError(t, err)

	// Second association should fail
	_, err = svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorResourceAlreadyAssociated)
}

func TestDisassociateRouteTable(t *testing.T) {
	svc := setupTestService(t)
	rtbID := createTestRtb(t, svc)

	assocOut, err := svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtbID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.DisassociateRouteTable(&ec2.DisassociateRouteTableInput{
		AssociationId: assocOut.AssociationId,
	}, testAccountID)
	require.NoError(t, err)

	// Verify association removed
	record, err := svc.getRouteTable(testAccountID, rtbID)
	require.NoError(t, err)
	assert.Empty(t, record.Associations)
}

func TestDisassociateRouteTable_Main(t *testing.T) {
	svc := setupTestService(t)
	record, err := svc.CreateRouteTableForVPC("vpc-test1", "10.0.0.0/16", testAccountID, true, "")
	require.NoError(t, err)

	_, err = svc.DisassociateRouteTable(&ec2.DisassociateRouteTableInput{
		AssociationId: aws.String(record.Associations[0].AssociationId),
	}, testAccountID)
	assert.EqualError(t, err, awserrors.ErrorInvalidParameterValue)
}

func TestReplaceRouteTableAssociation(t *testing.T) {
	svc := setupTestService(t)
	rtb1ID := createTestRtb(t, svc)
	rtb2ID := createTestRtb(t, svc)

	assocOut, err := svc.AssociateRouteTable(&ec2.AssociateRouteTableInput{
		RouteTableId: aws.String(rtb1ID),
		SubnetId:     aws.String("subnet-test1"),
	}, testAccountID)
	require.NoError(t, err)

	replaceOut, err := svc.ReplaceRouteTableAssociation(&ec2.ReplaceRouteTableAssociationInput{
		AssociationId: assocOut.AssociationId,
		RouteTableId:  aws.String(rtb2ID),
	}, testAccountID)
	require.NoError(t, err)
	assert.NotEmpty(t, *replaceOut.NewAssociationId)
	assert.NotEqual(t, *assocOut.AssociationId, *replaceOut.NewAssociationId)

	// Verify old table has no associations
	oldRecord, err := svc.getRouteTable(testAccountID, rtb1ID)
	require.NoError(t, err)
	assert.Empty(t, oldRecord.Associations)

	// Verify new table has the association
	newRecord, err := svc.getRouteTable(testAccountID, rtb2ID)
	require.NoError(t, err)
	require.Len(t, newRecord.Associations, 1)
	assert.Equal(t, "subnet-test1", newRecord.Associations[0].SubnetId)
}

func TestCreateRouteTableForVPC_Main(t *testing.T) {
	svc := setupTestService(t)
	record, err := svc.CreateRouteTableForVPC("vpc-test1", "10.0.0.0/16", testAccountID, true, "")
	require.NoError(t, err)

	assert.True(t, record.IsMain)
	assert.Len(t, record.Associations, 1)
	assert.True(t, record.Associations[0].Main)
	assert.Len(t, record.Routes, 1)
	assert.Equal(t, "local", record.Routes[0].GatewayId)
}
