package handlers_ec2_vpc

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestENI(t *testing.T, svc *VPCServiceImpl, subnetId string) string {
	t.Helper()
	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	return *out.NetworkInterface.NetworkInterfaceId
}

func TestCreateNetworkInterface(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	require.NotNil(t, out.NetworkInterface)

	eni := out.NetworkInterface
	assert.Equal(t, "eni-", (*eni.NetworkInterfaceId)[:4])
	assert.Equal(t, subnetId, *eni.SubnetId)
	assert.Equal(t, vpcId, *eni.VpcId)
	assert.Equal(t, "available", *eni.Status)
	assert.Equal(t, "10.0.1.4", *eni.PrivateIpAddress)
	assert.NotEmpty(t, *eni.MacAddress)
}

func TestCreateNetworkInterface_SequentialIPs(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	out1, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.4", *out1.NetworkInterface.PrivateIpAddress)

	out2, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.5", *out2.NetworkInterface.PrivateIpAddress)
}

func TestCreateNetworkInterface_MissingSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateNetworkInterface_InvalidSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String("subnet-nonexistent"),
	})
	assert.ErrorContains(t, err, "InvalidSubnetID.NotFound")
}

func TestCreateNetworkInterface_WithTags(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetId),
		Description: aws.String("test eni"),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("network-interface"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-eni")},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "test eni", *out.NetworkInterface.Description)
	require.Len(t, out.NetworkInterface.TagSet, 1)
	assert.Equal(t, "Name", *out.NetworkInterface.TagSet[0].Key)
	assert.Equal(t, "my-eni", *out.NetworkInterface.TagSet[0].Value)
}

func TestDeleteNetworkInterface(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	})
	require.NoError(t, err)

	// Verify deleted
	_, err = svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	})
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestDeleteNetworkInterface_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String("eni-nonexistent"),
	})
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestDeleteNetworkInterface_InUse(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	// Attach the ENI
	_, err := svc.AttachENI(eniId, "i-test123", 0)
	require.NoError(t, err)

	// Try to delete — should fail
	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	})
	assert.ErrorContains(t, err, "InvalidNetworkInterface.InUse")
}

func TestDeleteNetworkInterface_ReleasesIP(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	// Create and delete an ENI
	out1, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	ip1 := *out1.NetworkInterface.PrivateIpAddress

	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: out1.NetworkInterface.NetworkInterfaceId,
	})
	require.NoError(t, err)

	// Create another ENI — should reuse the released IP
	out2, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	})
	require.NoError(t, err)
	assert.Equal(t, ip1, *out2.NetworkInterface.PrivateIpAddress)
}

func TestDescribeNetworkInterfaces_All(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	createTestENI(t, svc, subnetId)
	createTestENI(t, svc, subnetId)

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{})
	require.NoError(t, err)
	assert.Len(t, out.NetworkInterfaces, 2)
}

func TestDescribeNetworkInterfaces_ByID(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	eniId := createTestENI(t, svc, subnetId)
	createTestENI(t, svc, subnetId) // second ENI

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	})
	require.NoError(t, err)
	assert.Len(t, out.NetworkInterfaces, 1)
	assert.Equal(t, eniId, *out.NetworkInterfaces[0].NetworkInterfaceId)
}

func TestDescribeNetworkInterfaces_FilterBySubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetA := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	subnetB := createTestSubnet(t, svc, vpcId, "10.0.2.0/24")

	createTestENI(t, svc, subnetA)
	createTestENI(t, svc, subnetB)

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("subnet-id"), Values: []*string{aws.String(subnetA)}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, out.NetworkInterfaces, 1)
	assert.Equal(t, subnetA, *out.NetworkInterfaces[0].SubnetId)
}

func TestDescribeNetworkInterfaces_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String("eni-nonexistent")},
	})
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestAttachENI(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	attachId, err := svc.AttachENI(eniId, "i-test123", 0)
	require.NoError(t, err)
	assert.Contains(t, attachId, "eni-attach-")

	// Verify status changed
	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	})
	require.NoError(t, err)
	eni := out.NetworkInterfaces[0]
	assert.Equal(t, "in-use", *eni.Status)
	assert.NotNil(t, eni.Attachment)
	assert.Equal(t, "i-test123", *eni.Attachment.InstanceId)
	assert.Equal(t, int64(0), *eni.Attachment.DeviceIndex)
}

func TestAttachENI_AlreadyAttached(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.AttachENI(eniId, "i-test123", 0)
	require.NoError(t, err)

	// Second attach should fail
	_, err = svc.AttachENI(eniId, "i-test456", 1)
	assert.ErrorContains(t, err, "InvalidNetworkInterface.InUse")
}

func TestDetachENI(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.AttachENI(eniId, "i-test123", 0)
	require.NoError(t, err)

	err = svc.DetachENI(eniId)
	require.NoError(t, err)

	// Verify status changed back
	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	})
	require.NoError(t, err)
	assert.Equal(t, "available", *out.NetworkInterfaces[0].Status)
	assert.Nil(t, out.NetworkInterfaces[0].Attachment)
}

func TestGenerateENIMac(t *testing.T) {
	mac := generateENIMac("eni-test123")
	assert.Regexp(t, `^02:00:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$`, mac)

	// Same input produces same MAC
	assert.Equal(t, mac, generateENIMac("eni-test123"))

	// Different input produces different MAC
	assert.NotEqual(t, mac, generateENIMac("eni-test456"))
}
