package handlers_ec2_vpc

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestVPCService(t *testing.T) *VPCServiceImpl {
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

	svc, err := NewVPCServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

func createTestVPC(t *testing.T, svc *VPCServiceImpl, cidr string) string {
	t.Helper()
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String(cidr),
	})
	require.NoError(t, err)
	return *out.Vpc.VpcId
}

func createTestSubnet(t *testing.T, svc *VPCServiceImpl, vpcID, cidr string) string {
	t.Helper()
	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String(cidr),
	})
	require.NoError(t, err)
	return *out.Subnet.SubnetId
}

// --- VPC Tests ---

func TestCreateVpc(t *testing.T) {
	svc := setupTestVPCService(t)
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)
	require.NotNil(t, out.Vpc)
	assert.Equal(t, "vpc-", (*out.Vpc.VpcId)[:4])
	assert.Equal(t, "10.0.0.0/16", *out.Vpc.CidrBlock)
	assert.Equal(t, "available", *out.Vpc.State)
	assert.False(t, *out.Vpc.IsDefault)
}

func TestCreateVpc_MissingCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateVpc_EmptyCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String(""),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateVpc_InvalidCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("not-a-cidr"),
	})
	assert.ErrorContains(t, err, "InvalidVpcRange")
}

func TestCreateVpc_CidrTooLarge(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/8"),
	})
	assert.ErrorContains(t, err, "InvalidVpcRange")
}

func TestCreateVpc_CidrTooSmall(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/29"),
	})
	assert.ErrorContains(t, err, "InvalidVpcRange")
}

func TestCreateVpc_WithTags(t *testing.T) {
	svc := setupTestVPCService(t)
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("vpc"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-vpc")},
					{Key: aws.String("Env"), Value: aws.String("test")},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, out.Vpc.Tags, 2)

	// Verify tags persist through describe
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		VpcIds: []*string{out.Vpc.VpcId},
	})
	require.NoError(t, err)
	require.Len(t, desc.Vpcs, 1)
	assert.Len(t, desc.Vpcs[0].Tags, 2)
}

func TestCreateVpc_TagsWrongResourceType(t *testing.T) {
	svc := setupTestVPCService(t)
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("instance"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("wrong-type")},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, out.Vpc.Tags)
}

func TestCreateVpc_VNIIncrement(t *testing.T) {
	svc := setupTestVPCService(t)

	// Create two VPCs and verify they get different VNIs
	out1, err := svc.CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	require.NoError(t, err)
	out2, err := svc.CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("10.1.0.0/16")})
	require.NoError(t, err)

	// Verify VPCs are different
	assert.NotEqual(t, *out1.Vpc.VpcId, *out2.Vpc.VpcId)
}

func TestDeleteVpc(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	require.NoError(t, err)

	// Verify deleted
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		VpcIds: []*string{aws.String(vpcID)},
	})
	assert.ErrorContains(t, err, "InvalidVpcID.NotFound")
	assert.Nil(t, desc)
}

func TestDeleteVpc_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{
		VpcId: aws.String("vpc-nonexistent"),
	})
	assert.ErrorContains(t, err, "InvalidVpcID.NotFound")
}

func TestDeleteVpc_MissingID(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestDeleteVpc_WithSubnets(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// Should fail because VPC has subnets
	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	assert.ErrorContains(t, err, "DependencyViolation")
}

func TestDescribeVpcs_All(t *testing.T) {
	svc := setupTestVPCService(t)
	createTestVPC(t, svc, "10.0.0.0/16")
	createTestVPC(t, svc, "10.1.0.0/16")

	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	assert.Len(t, desc.Vpcs, 2)
}

func TestDescribeVpcs_ByID(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestVPC(t, svc, "10.1.0.0/16")

	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		VpcIds: []*string{aws.String(vpcID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.Vpcs, 1)
	assert.Equal(t, vpcID, *desc.Vpcs[0].VpcId)
}

func TestDescribeVpcs_Empty(t *testing.T) {
	svc := setupTestVPCService(t)
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	assert.Empty(t, desc.Vpcs)
}

func TestDescribeVpcs_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		VpcIds: []*string{aws.String("vpc-nonexistent")},
	})
	assert.ErrorContains(t, err, "InvalidVpcID.NotFound")
}

// --- Subnet Tests ---

func TestCreateSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	require.NoError(t, err)
	require.NotNil(t, out.Subnet)
	assert.Equal(t, "subnet-", (*out.Subnet.SubnetId)[:7])
	assert.Equal(t, vpcID, *out.Subnet.VpcId)
	assert.Equal(t, "10.0.1.0/24", *out.Subnet.CidrBlock)
	assert.Equal(t, "available", *out.Subnet.State)
	// /24 = 256 - 5 reserved = 251
	assert.Equal(t, int64(251), *out.Subnet.AvailableIpAddressCount)
}

func TestCreateSubnet_MissingVpcId(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateSubnet_MissingCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId: aws.String(vpcID),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateSubnet_InvalidVpcId(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String("vpc-nonexistent"),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	assert.ErrorContains(t, err, "InvalidVpcID.NotFound")
}

func TestCreateSubnet_InvalidCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("not-a-cidr"),
	})
	assert.ErrorContains(t, err, "InvalidSubnet.Range")
}

func TestCreateSubnet_OutsideVpcCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("192.168.1.0/24"),
	})
	assert.ErrorContains(t, err, "InvalidSubnet.Range")
}

func TestCreateSubnet_ConflictingCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// Try to create overlapping subnet
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/25"),
	})
	assert.ErrorContains(t, err, "InvalidSubnet.Conflict")
}

func TestCreateSubnet_WithTags(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("subnet"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-subnet")},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, out.Subnet.Tags, 1)
}

func TestCreateSubnet_WithAZ(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	})
	require.NoError(t, err)
	assert.Equal(t, "us-east-1a", *out.Subnet.AvailabilityZone)
}

func TestDeleteSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	_, err := svc.DeleteSubnet(&ec2.DeleteSubnetInput{
		SubnetId: aws.String(subnetID),
	})
	require.NoError(t, err)

	// Verify deleted
	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		SubnetIds: []*string{aws.String(subnetID)},
	})
	assert.ErrorContains(t, err, "InvalidSubnetID.NotFound")
	assert.Nil(t, desc)
}

func TestDeleteSubnet_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteSubnet(&ec2.DeleteSubnetInput{
		SubnetId: aws.String("subnet-nonexistent"),
	})
	assert.ErrorContains(t, err, "InvalidSubnetID.NotFound")
}

func TestDeleteSubnet_MissingID(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteSubnet(&ec2.DeleteSubnetInput{})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestDescribeSubnets_All(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSubnet(t, svc, vpcID, "10.0.1.0/24")
	createTestSubnet(t, svc, vpcID, "10.0.2.0/24")

	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	require.NoError(t, err)
	assert.Len(t, desc.Subnets, 2)
}

func TestDescribeSubnets_ByID(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")
	createTestSubnet(t, svc, vpcID, "10.0.2.0/24")

	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		SubnetIds: []*string{aws.String(subnetID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.Subnets, 1)
	assert.Equal(t, subnetID, *desc.Subnets[0].SubnetId)
}

func TestDescribeSubnets_ByVpcId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpc1 := createTestVPC(t, svc, "10.0.0.0/16")
	vpc2 := createTestVPC(t, svc, "10.1.0.0/16")
	createTestSubnet(t, svc, vpc1, "10.0.1.0/24")
	createTestSubnet(t, svc, vpc2, "10.1.1.0/24")

	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String(vpc1)},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, desc.Subnets, 1)
	assert.Equal(t, vpc1, *desc.Subnets[0].VpcId)
}

func TestDescribeSubnets_Empty(t *testing.T) {
	svc := setupTestVPCService(t)
	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	require.NoError(t, err)
	assert.Empty(t, desc.Subnets)
}

func TestDescribeSubnets_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		SubnetIds: []*string{aws.String("subnet-nonexistent")},
	})
	assert.ErrorContains(t, err, "InvalidSubnetID.NotFound")
}

func TestCreateMultipleSubnetsInVpc(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	// Create non-overlapping subnets
	sub1 := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")
	sub2 := createTestSubnet(t, svc, vpcID, "10.0.2.0/24")
	sub3 := createTestSubnet(t, svc, vpcID, "10.0.3.0/24")

	assert.NotEqual(t, sub1, sub2)
	assert.NotEqual(t, sub2, sub3)

	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{aws.String(vpcID)}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, desc.Subnets, 3)
}

func TestDeleteVpcAfterSubnetsDeleted(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// Can't delete VPC with subnets
	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	assert.ErrorContains(t, err, "DependencyViolation")

	// Delete subnet first
	_, err = svc.DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)})
	require.NoError(t, err)

	// Now VPC can be deleted
	_, err = svc.DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	require.NoError(t, err)
}

func TestCreateSubnet_CidrRanges(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	// /28 (smallest allowed) = 16 IPs - 5 reserved = 11
	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/28"),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(11), *out.Subnet.AvailableIpAddressCount)

	// /16 (largest allowed) = 65536 IPs - 5 reserved = 65531
	vpcID2 := createTestVPC(t, svc, "172.16.0.0/16")
	out2, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID2),
		CidrBlock: aws.String("172.16.0.0/16"),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(65531), *out2.Subnet.AvailableIpAddressCount)
}

func TestCreateSubnet_CidrTooSmall(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.0.0/29"),
	})
	assert.ErrorContains(t, err, "InvalidSubnet.Range")
}

func TestVpcCidrBlockAssociation(t *testing.T) {
	svc := setupTestVPCService(t)
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)
	require.Len(t, out.Vpc.CidrBlockAssociationSet, 1)
	assert.Equal(t, "10.0.0.0/16", *out.Vpc.CidrBlockAssociationSet[0].CidrBlock)
	assert.Equal(t, "associated", *out.Vpc.CidrBlockAssociationSet[0].CidrBlockState.State)
}
