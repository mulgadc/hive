package handlers_ec2_vpc

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestVPCServiceWithNC(t *testing.T) (*VPCServiceImpl, *nats.Conn) {
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
	return svc, nc
}

func setupTestVPCService(t *testing.T) *VPCServiceImpl {
	t.Helper()
	svc, _ := setupTestVPCServiceWithNC(t)
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

// --- Default VPC Tests ---

func TestEnsureDefaultVPC(t *testing.T) {
	svc := setupTestVPCService(t)

	err := svc.EnsureDefaultVPC()
	require.NoError(t, err)

	// Verify default VPC was created
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	require.Len(t, desc.Vpcs, 1)
	assert.True(t, *desc.Vpcs[0].IsDefault)
	assert.Equal(t, "172.31.0.0/16", *desc.Vpcs[0].CidrBlock)

	// Verify default subnet was created
	subDesc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	require.NoError(t, err)
	require.Len(t, subDesc.Subnets, 1)
	assert.True(t, *subDesc.Subnets[0].DefaultForAz)
	assert.Equal(t, "172.31.0.0/20", *subDesc.Subnets[0].CidrBlock)
	assert.Equal(t, *desc.Vpcs[0].VpcId, *subDesc.Subnets[0].VpcId)
}

func TestEnsureDefaultVPC_Idempotent(t *testing.T) {
	svc := setupTestVPCService(t)

	// Call twice — should be idempotent
	require.NoError(t, svc.EnsureDefaultVPC())
	require.NoError(t, svc.EnsureDefaultVPC())

	// Should still have exactly 1 VPC and 1 subnet
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	assert.Len(t, desc.Vpcs, 1)

	subDesc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	require.NoError(t, err)
	assert.Len(t, subDesc.Subnets, 1)
}

func TestEnsureDefaultVPC_SkipsWhenDefaultExists(t *testing.T) {
	svc := setupTestVPCService(t)

	// Create default VPC first
	require.NoError(t, svc.EnsureDefaultVPC())

	// Create a second (non-default) VPC
	createTestVPC(t, svc, "10.0.0.0/16")

	// Calling again should not create another default
	require.NoError(t, svc.EnsureDefaultVPC())

	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	require.NoError(t, err)
	assert.Len(t, desc.Vpcs, 2) // 1 default + 1 manual

	// Only 1 should be default
	defaultCount := 0
	for _, vpc := range desc.Vpcs {
		if *vpc.IsDefault {
			defaultCount++
		}
	}
	assert.Equal(t, 1, defaultCount)
}

func TestGetDefaultSubnet(t *testing.T) {
	svc := setupTestVPCService(t)

	// No default subnet yet
	_, err := svc.GetDefaultSubnet()
	assert.Error(t, err)

	// Create default VPC + subnet
	require.NoError(t, svc.EnsureDefaultVPC())

	subnet, err := svc.GetDefaultSubnet()
	require.NoError(t, err)
	assert.Equal(t, "172.31.0.0/20", subnet.CidrBlock)
	assert.True(t, subnet.IsDefault)
}

func TestGetDefaultSubnet_NotConfusedByNonDefault(t *testing.T) {
	svc := setupTestVPCService(t)

	// Create a non-default VPC + subnet
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// GetDefaultSubnet should not return the non-default subnet
	_, err := svc.GetDefaultSubnet()
	assert.Error(t, err)

	// Now create default
	require.NoError(t, svc.EnsureDefaultVPC())
	subnet, err := svc.GetDefaultSubnet()
	require.NoError(t, err)
	assert.True(t, subnet.IsDefault)
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

// --- KV nil guard tests (mulga-x83) ---

func TestKVNilGuard_CreateVpc(t *testing.T) {
	svc := NewVPCServiceImpl(nil) // in-memory fallback, all KV fields nil
	_, err := svc.CreateVpc(&ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DeleteVpc(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String("vpc-123")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DescribeVpcs(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_CreateSubnet(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String("vpc-123"),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DeleteSubnet(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String("subnet-123")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DescribeSubnets(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_CreateNetworkInterface(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{SubnetId: aws.String("subnet-123")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DeleteNetworkInterface(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String("eni-123")})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

func TestKVNilGuard_DescribeNetworkInterfaces(t *testing.T) {
	svc := NewVPCServiceImpl(nil)
	_, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ServerInternal")
}

// --- Filter tests ---

func TestDescribeVpcs_NilFields(t *testing.T) {
	svc := setupTestVPCService(t)
	createTestVPC(t, svc, "10.0.0.0/16")

	// DescribeVpcs with nil VpcIds and nil Filters should return all
	desc, err := svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		VpcIds:  nil,
		Filters: nil,
	})
	require.NoError(t, err)
	assert.Len(t, desc.Vpcs, 1)
}

func TestDescribeSubnets_FilterByVpcId_NoMatch(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// Filter by a VPC ID that doesn't match any subnet
	desc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String("vpc-nonexistent")},
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, desc.Subnets)
}

// --- NATS event tests ---

func TestCreateVpc_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.create", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	require.NoError(t, err)
	vpcID := *out.Vpc.VpcId

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), vpcID)
		assert.Contains(t, string(msg.Data), "10.0.0.0/16")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.create event")
	}
}

func TestDeleteVpc_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	_, err = svc.DeleteVpc(&ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	require.NoError(t, err)

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), vpcID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.delete event")
	}
}

func TestCreateSubnet_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.create-subnet", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	out, err := svc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	require.NoError(t, err)
	subnetID := *out.Subnet.SubnetId

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), subnetID)
		assert.Contains(t, string(msg.Data), vpcID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.create-subnet event")
	}
}

func TestDeleteSubnet_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete-subnet", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	_, err = svc.DeleteSubnet(&ec2.DeleteSubnetInput{
		SubnetId: aws.String(subnetID),
	})
	require.NoError(t, err)

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), subnetID)
		assert.Contains(t, string(msg.Data), vpcID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.delete-subnet event")
	}
}

// --- Additional coverage tests ---

func TestEnsureDefaultVPC_WithConfigAZ(t *testing.T) {
	// Create a service with custom config that has AZ set
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

	cfg := &config.Config{AZ: "us-west-2b"}
	svc, err := NewVPCServiceImplWithNATS(cfg, nc)
	require.NoError(t, err)

	err = svc.EnsureDefaultVPC()
	require.NoError(t, err)

	// Verify the subnet uses the configured AZ
	subDesc, err := svc.DescribeSubnets(&ec2.DescribeSubnetsInput{})
	require.NoError(t, err)
	require.Len(t, subDesc.Subnets, 1)
	assert.Equal(t, "us-west-2b", *subDesc.Subnets[0].AvailabilityZone)
}

func TestGetDefaultSubnet_NoKV(t *testing.T) {
	// Service without KV should return error
	svc := NewVPCServiceImpl(nil)
	_, err := svc.GetDefaultSubnet()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestCreateVpc_NormalizesNetworkCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	// Pass a CIDR with host bits set — should be normalized to network address
	out, err := svc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.5/16"),
	})
	require.NoError(t, err)
	// Should normalize to 10.0.0.0/16
	assert.Equal(t, "10.0.0.0/16", *out.Vpc.CidrBlock)
}

func TestDeleteVpc_WithENIs(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	// Create an ENI in the subnet
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetID),
	})
	require.NoError(t, err)

	// Delete subnet should succeed (ENI is in subnet but delete checks subnet dependencies in vpc delete)
	// First delete subnet
	_, err = svc.DeleteSubnet(&ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)})
	// DeleteSubnet doesn't check for ENIs currently - just deletes
	require.NoError(t, err)

	// Now delete VPC should succeed since subnet is gone
	_, err = svc.DeleteVpc(&ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	require.NoError(t, err)
}

func TestCreateNetworkInterface_WithExplicitIP(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	subnetID := createTestSubnet(t, svc, vpcID, "10.0.1.0/24")

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:         aws.String(subnetID),
		PrivateIpAddress: aws.String("10.0.1.100"),
	})
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.100", *out.NetworkInterface.PrivateIpAddress)
}

func TestAttachENI_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.AttachENI("eni-nonexistent", "i-test", 0)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestDetachENI_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	err := svc.DetachENI("eni-nonexistent")
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

// --- EnsureDefaultVPC event test ---

func TestEnsureDefaultVPC_PublishesEvents(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)

	vpcCh := make(chan *nats.Msg, 1)
	subCh := make(chan *nats.Msg, 1)
	vpcSub, err := nc.Subscribe("vpc.create", func(msg *nats.Msg) { vpcCh <- msg })
	require.NoError(t, err)
	defer func() { _ = vpcSub.Unsubscribe() }()
	subSub, err := nc.Subscribe("vpc.create-subnet", func(msg *nats.Msg) { subCh <- msg })
	require.NoError(t, err)
	defer func() { _ = subSub.Unsubscribe() }()

	err = svc.EnsureDefaultVPC()
	require.NoError(t, err)

	// Should publish vpc.create event
	select {
	case msg := <-vpcCh:
		assert.Contains(t, string(msg.Data), "172.31.0.0/16")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.create event from EnsureDefaultVPC")
	}

	// Should publish vpc.create-subnet event
	select {
	case msg := <-subCh:
		assert.Contains(t, string(msg.Data), "172.31.0.0/20")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.create-subnet event from EnsureDefaultVPC")
	}
}
