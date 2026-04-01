package handlers_elbv2

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elbv2"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestServiceWithVPC creates an ELBv2 service wired to a real VPC service
// with a pre-created VPC and subnet for ENI allocation testing.
func setupTestServiceWithVPC(t *testing.T) (*ELBv2ServiceImpl, *handlers_ec2_vpc.VPCServiceImpl) {
	t.Helper()
	_, nc, _ := testutil.StartTestJetStream(t)

	// Create VPC service
	vpcSvc, err := handlers_ec2_vpc.NewVPCServiceImplWithNATS(nil, nc)
	require.NoError(t, err)

	// Create ELBv2 service with VPC wired in
	elbv2Svc, err := NewELBv2ServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	elbv2Svc.SetVPCService(vpcSvc)

	// Create a VPC and subnet for tests
	vpcOut, err := vpcSvc.CreateVpc(&ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = vpcSvc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:            vpcOut.Vpc.VpcId,
		CidrBlock:        aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	}, testAccountID)
	require.NoError(t, err)

	return elbv2Svc, vpcSvc
}

// getTestSubnetID creates a fresh subnet and returns its ID.
func getTestSubnetID(t *testing.T, vpcSvc *handlers_ec2_vpc.VPCServiceImpl, vpcID, cidr, az string) string {
	t.Helper()
	out, err := vpcSvc.CreateSubnet(&ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String(cidr),
		AvailabilityZone: aws.String(az),
	}, testAccountID)
	require.NoError(t, err)
	return *out.Subnet.SubnetId
}

func TestCreateLoadBalancer_CreatesENIs(t *testing.T) {
	svc, vpcSvc := setupTestServiceWithVPC(t)

	// Find the subnet we created
	subnets, err := vpcSvc.DescribeSubnets(&ec2.DescribeSubnetsInput{}, testAccountID)
	require.NoError(t, err)
	require.NotEmpty(t, subnets.Subnets)
	subnetID := *subnets.Subnets[0].SubnetId

	// Create ALB with subnet
	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("eni-test-alb"),
		Subnets: []*string{aws.String(subnetID)},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.LoadBalancers, 1)
	lb := out.LoadBalancers[0]

	// Verify VpcId was populated from subnet
	assert.NotEmpty(t, *lb.VpcId)

	// Verify AZ info was populated
	require.Len(t, lb.AvailabilityZones, 1)
	assert.Equal(t, "us-east-1a", *lb.AvailabilityZones[0].ZoneName)
	assert.Equal(t, subnetID, *lb.AvailabilityZones[0].SubnetId)

	// Verify ENI was created
	eniDesc, err := vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	require.NoError(t, err)
	require.Len(t, eniDesc.NetworkInterfaces, 1)

	eni := eniDesc.NetworkInterfaces[0]
	assert.Contains(t, *eni.Description, "ELB app/eni-test-alb/")
	assert.True(t, *eni.RequesterManaged)
	assert.Equal(t, subnetID, *eni.SubnetId)
	assert.NotEmpty(t, *eni.PrivateIpAddress)

	// Verify ENI has the managed-by tag
	foundTag := false
	for _, tag := range eni.TagSet {
		if *tag.Key == "spinifex:managed-by" && *tag.Value == "elbv2" {
			foundTag = true
		}
	}
	assert.True(t, foundTag, "ENI should have spinifex:managed-by=elbv2 tag")
}

func TestCreateLoadBalancer_MultipleSubnets(t *testing.T) {
	svc, vpcSvc := setupTestServiceWithVPC(t)

	// Get VPC ID
	vpcs, _ := vpcSvc.DescribeVpcs(&ec2.DescribeVpcsInput{}, testAccountID)
	vpcID := *vpcs.Vpcs[0].VpcId

	// Create two subnets in different AZs
	sub1 := getTestSubnetID(t, vpcSvc, vpcID, "10.0.2.0/24", "us-east-1a")
	sub2 := getTestSubnetID(t, vpcSvc, vpcID, "10.0.3.0/24", "us-east-1b")

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("multi-subnet-alb"),
		Subnets: []*string{aws.String(sub1), aws.String(sub2)},
	}, testAccountID)
	require.NoError(t, err)

	lb := out.LoadBalancers[0]
	assert.Len(t, lb.AvailabilityZones, 2)

	// Verify 2 ENIs created (+ 1 from setupTestServiceWithVPC's initial subnet)
	eniDesc, _ := vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	managedCount := 0
	for _, eni := range eniDesc.NetworkInterfaces {
		if eni.RequesterManaged != nil && *eni.RequesterManaged {
			managedCount++
		}
	}
	assert.Equal(t, 2, managedCount)
}

func TestDeleteLoadBalancer_CleansUpENIs(t *testing.T) {
	svc, vpcSvc := setupTestServiceWithVPC(t)

	subnets, _ := vpcSvc.DescribeSubnets(&ec2.DescribeSubnetsInput{}, testAccountID)
	subnetID := *subnets.Subnets[0].SubnetId

	// Create and then delete ALB
	lbOut, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("cleanup-alb"),
		Subnets: []*string{aws.String(subnetID)},
	}, testAccountID)
	require.NoError(t, err)

	// Verify ENI exists
	eniDesc, _ := vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	assert.Len(t, eniDesc.NetworkInterfaces, 1)

	// Delete ALB
	_, err = svc.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: lbOut.LoadBalancers[0].LoadBalancerArn,
	}, testAccountID)
	require.NoError(t, err)

	// Verify ENI was cleaned up
	eniDesc, _ = vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	assert.Empty(t, eniDesc.NetworkInterfaces)
}

func TestCreateLoadBalancer_InvalidSubnet(t *testing.T) {
	svc, _ := setupTestServiceWithVPC(t)

	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("bad-subnet-alb"),
		Subnets: []*string{aws.String("subnet-nonexistent")},
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SubnetNotFound")
}

func TestCreateLoadBalancer_RollbackOnPartialFailure(t *testing.T) {
	svc, vpcSvc := setupTestServiceWithVPC(t)

	subnets, _ := vpcSvc.DescribeSubnets(&ec2.DescribeSubnetsInput{}, testAccountID)
	validSubnet := *subnets.Subnets[0].SubnetId

	// First subnet valid, second invalid — should rollback the first ENI
	_, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("rollback-alb"),
		Subnets: []*string{aws.String(validSubnet), aws.String("subnet-bogus")},
	}, testAccountID)
	assert.Error(t, err)

	// Verify no orphaned ENIs remain
	eniDesc, _ := vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	assert.Empty(t, eniDesc.NetworkInterfaces)
}

func TestCreateLoadBalancer_WithoutVPCService(t *testing.T) {
	// When vpcService is nil (e.g. in pure unit tests), ENI creation is skipped
	svc := setupTestService(t)

	out, err := svc.CreateLoadBalancer(&elbv2.CreateLoadBalancerInput{
		Name:    aws.String("no-vpc-alb"),
		Subnets: []*string{aws.String("subnet-xxx")},
	}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, out.LoadBalancers[0].VpcId)
	assert.Empty(t, out.LoadBalancers[0].AvailabilityZones)
}

func TestENI_RequesterManagedFlag(t *testing.T) {
	_, vpcSvc := setupTestServiceWithVPC(t)

	subnets, _ := vpcSvc.DescribeSubnets(&ec2.DescribeSubnetsInput{}, testAccountID)
	subnetID := *subnets.Subnets[0].SubnetId

	// Create a normal ENI (not managed)
	normalENI, err := vpcSvc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetID),
		Description: aws.String("user ENI"),
	}, testAccountID)
	require.NoError(t, err)

	// Create a managed ENI (like ALB would)
	managedENI, err := vpcSvc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetID),
		Description: aws.String("ELB app/test/lb123"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("network-interface"),
				Tags: []*ec2.Tag{
					{Key: aws.String("spinifex:managed-by"), Value: aws.String("elbv2")},
				},
			},
		},
	}, testAccountID)
	require.NoError(t, err)

	// Describe all ENIs
	desc, _ := vpcSvc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
	require.Len(t, desc.NetworkInterfaces, 2)

	for _, eni := range desc.NetworkInterfaces {
		if *eni.NetworkInterfaceId == *normalENI.NetworkInterface.NetworkInterfaceId {
			assert.False(t, *eni.RequesterManaged, "normal ENI should not be RequesterManaged")
		}
		if *eni.NetworkInterfaceId == *managedENI.NetworkInterface.NetworkInterfaceId {
			assert.True(t, *eni.RequesterManaged, "managed ENI should be RequesterManaged")
		}
	}
}
