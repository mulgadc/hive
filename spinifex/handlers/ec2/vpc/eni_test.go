package handlers_ec2_vpc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestENI(t *testing.T, svc *VPCServiceImpl, subnetId string) string {
	t.Helper()
	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	require.NoError(t, err)
	return *out.NetworkInterface.NetworkInterfaceId
}

func TestCreateNetworkInterface(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
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
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.4", *out1.NetworkInterface.PrivateIpAddress)

	out2, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.5", *out2.NetworkInterface.PrivateIpAddress)
}

func TestCreateNetworkInterface_MissingSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{}, testAccountID)
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateNetworkInterface_InvalidSubnet(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String("subnet-nonexistent"),
	}, testAccountID)
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
	}, testAccountID)
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
	}, testAccountID)
	require.NoError(t, err)

	// Verify deleted
	_, err = svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestDeleteNetworkInterface_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String("eni-nonexistent"),
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestDeleteNetworkInterface_InUse(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	// Attach the ENI
	_, err := svc.AttachENI(testAccountID, eniId, "i-test123", 0)
	require.NoError(t, err)

	// Try to delete — should fail
	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterface.InUse")
}

func TestDeleteNetworkInterface_ReleasesIP(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	// Create and delete an ENI
	out1, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	require.NoError(t, err)
	ip1 := *out1.NetworkInterface.PrivateIpAddress

	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: out1.NetworkInterface.NetworkInterfaceId,
	}, testAccountID)
	require.NoError(t, err)

	// Create another ENI — should reuse the released IP
	out2, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, ip1, *out2.NetworkInterface.PrivateIpAddress)
}

func TestDescribeNetworkInterfaces_All(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	createTestENI(t, svc, subnetId)
	createTestENI(t, svc, subnetId)

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{}, testAccountID)
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
	}, testAccountID)
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
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.NetworkInterfaces, 1)
	assert.Equal(t, subnetA, *out.NetworkInterfaces[0].SubnetId)
}

func TestDescribeNetworkInterfaces_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)
	_, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String("eni-nonexistent")},
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestAttachENI(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	attachId, err := svc.AttachENI(testAccountID, eniId, "i-test123", 0)
	require.NoError(t, err)
	assert.Contains(t, attachId, "eni-attach-")

	// Verify status changed
	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
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

	_, err := svc.AttachENI(testAccountID, eniId, "i-test123", 0)
	require.NoError(t, err)

	// Second attach should fail
	_, err = svc.AttachENI(testAccountID, eniId, "i-test456", 1)
	assert.ErrorContains(t, err, "InvalidNetworkInterface.InUse")
}

func TestDetachENI(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.AttachENI(testAccountID, eniId, "i-test123", 0)
	require.NoError(t, err)

	err = svc.DetachENI(testAccountID, eniId)
	require.NoError(t, err)

	// Verify status changed back
	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
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

// --- Filter tests ---

func TestDescribeNetworkInterfaces_FilterByVpcId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpc1 := createTestVPC(t, svc, "10.0.0.0/16")
	vpc2 := createTestVPC(t, svc, "10.1.0.0/16")
	subnet1 := createTestSubnet(t, svc, vpc1, "10.0.1.0/24")
	subnet2 := createTestSubnet(t, svc, vpc2, "10.1.1.0/24")

	createTestENI(t, svc, subnet1)
	createTestENI(t, svc, subnet2)

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{aws.String(vpc1)}},
		},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.NetworkInterfaces, 1)
	assert.Equal(t, vpc1, *out.NetworkInterfaces[0].VpcId)
}

func TestDescribeNetworkInterfaces_FilterByAttachmentInstanceId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	eni1 := createTestENI(t, svc, subnetId)
	createTestENI(t, svc, subnetId) // second ENI, not attached

	// Attach first ENI to an instance
	_, err := svc.AttachENI(testAccountID, eni1, "i-attached", 0)
	require.NoError(t, err)

	out, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("attachment.instance-id"), Values: []*string{aws.String("i-attached")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.NetworkInterfaces, 1)
	assert.Equal(t, eni1, *out.NetworkInterfaces[0].NetworkInterfaceId)
}

func TestDescribeNetworkInterfaces_FilterByDescription(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	// Create two ENIs with different descriptions
	out1, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetId),
		Description: aws.String("ELB app/my-alb/lb-123"),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetId),
		Description: aws.String("regular ENI"),
	}, testAccountID)
	require.NoError(t, err)

	// Filter by description should return only the ALB ENI
	desc, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("description"), Values: []*string{aws.String("ELB app/my-alb/lb-123")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.NetworkInterfaces, 1)
	assert.Equal(t, *out1.NetworkInterface.NetworkInterfaceId, *desc.NetworkInterfaces[0].NetworkInterfaceId)
}

func TestCreateNetworkInterface_IPExhaustion(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	// /28 subnet: 16 IPs total, 4 reserved at start + 1 broadcast = 11 usable
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.0.0/28")

	// Allocate all 11 available IPs
	for i := range 11 {
		_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
			SubnetId: aws.String(subnetId),
		}, testAccountID)
		require.NoError(t, err, "ENI %d should succeed", i)
	}

	// 12th allocation should fail — subnet exhausted
	_, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	assert.Error(t, err)
}

// --- NATS event tests ---

func TestCreateNetworkInterface_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.create-port", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
	}, testAccountID)
	require.NoError(t, err)
	eniId := *out.NetworkInterface.NetworkInterfaceId

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), eniId)
		assert.Contains(t, string(msg.Data), subnetId)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.create-port event")
	}
}

func TestDeleteNetworkInterface_PublishesEvent(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete-port", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	require.NoError(t, err)

	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), eniId)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.delete-port event")
	}
}

func TestModifyNetworkInterfaceAttribute_SecurityGroups(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.ModifyNetworkInterfaceAttribute(&ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(eniId),
		Groups:             []*string{aws.String("sg-111"), aws.String("sg-222")},
	}, testAccountID)
	require.NoError(t, err)

	desc, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.NetworkInterfaces, 1)
	require.Len(t, desc.NetworkInterfaces[0].Groups, 2)
	assert.Equal(t, "sg-111", *desc.NetworkInterfaces[0].Groups[0].GroupId)
	assert.Equal(t, "sg-222", *desc.NetworkInterfaces[0].Groups[1].GroupId)
}

func TestModifyNetworkInterfaceAttribute_Description(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.ModifyNetworkInterfaceAttribute(&ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(eniId),
		Description:        &ec2.AttributeValue{Value: aws.String("updated description")},
	}, testAccountID)
	require.NoError(t, err)

	desc, err := svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, desc.NetworkInterfaces, 1)
	assert.Equal(t, "updated description", *desc.NetworkInterfaces[0].Description)
}

func TestModifyNetworkInterfaceAttribute_NoAttributes(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	_, err := svc.ModifyNetworkInterfaceAttribute(&ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidParameterValue")
}

func TestModifyNetworkInterfaceAttribute_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.ModifyNetworkInterfaceAttribute(&ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String("eni-nonexistent"),
		Groups:             []*string{aws.String("sg-111")},
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestModifyNetworkInterfaceAttribute_MissingID(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.ModifyNetworkInterfaceAttribute(&ec2.ModifyNetworkInterfaceAttributeInput{
		Groups: []*string{aws.String("sg-111")},
	}, testAccountID)
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestCreateNetworkInterface_WithSecurityGroups(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")

	out, err := svc.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId: aws.String(subnetId),
		Groups:   []*string{aws.String("sg-aaa"), aws.String("sg-bbb")},
	}, testAccountID)
	require.NoError(t, err)
	require.Len(t, out.NetworkInterface.Groups, 2)
	assert.Equal(t, "sg-aaa", *out.NetworkInterface.Groups[0].GroupId)
	assert.Equal(t, "sg-bbb", *out.NetworkInterface.Groups[1].GroupId)
}

func TestDeleteNetworkInterface_ReleasesPublicIP(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	// Set up external IPAM with a static pool
	js, err := nc.JetStream()
	require.NoError(t, err)
	ipamKV, err := utils.GetOrCreateKVBucket(js, KVBucketExternalIPAM, 5)
	require.NoError(t, err)
	pools := []ExternalPoolConfig{{
		Name:       "test-pool",
		Source:     "static",
		RangeStart: "203.0.113.10",
		RangeEnd:   "203.0.113.20",
		Gateway:    "203.0.113.1",
		PrefixLen:  24,
		Region:     "us-east-1",
		AZ:         "us-east-1a",
	}}
	extIPAM := NewExternalIPAMWithKV(ipamKV, pools)
	require.NoError(t, extIPAM.initPools())

	// Allocate a public IP and assign it to the ENI
	publicIP, poolName, err := extIPAM.AllocateIP("us-east-1", "us-east-1a", "auto_assign", eniId, "i-test")
	require.NoError(t, err)
	require.NoError(t, svc.UpdateENIPublicIP(testAccountID, eniId, publicIP, poolName))

	// Inject external IPAM (no EIP KV — all public IPs are auto-assigned)
	svc.SetExternalIPAM(extIPAM, nil)

	// Subscribe to vpc.delete-nat to verify event is published
	natCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete-nat", func(msg *nats.Msg) {
		natCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	// Delete the ENI
	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	require.NoError(t, err)

	// Verify vpc.delete-nat event was published with correct public IP
	select {
	case msg := <-natCh:
		var evt struct {
			ExternalIP string `json:"external_ip"`
			VpcId      string `json:"vpc_id"`
		}
		require.NoError(t, json.Unmarshal(msg.Data, &evt))
		assert.Equal(t, publicIP, evt.ExternalIP)
		assert.Equal(t, vpcId, evt.VpcId)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for vpc.delete-nat event")
	}

	// Verify the public IP was released back to the pool — allocate again and
	// confirm we get the same IP (it was the first in the range)
	reusedIP, _, err := extIPAM.AllocateIP("us-east-1", "us-east-1a", "auto_assign", "eni-other", "i-other")
	require.NoError(t, err)
	assert.Equal(t, publicIP, reusedIP)
}

func TestDeleteNetworkInterface_SkipsEIPOwnedPublicIP(t *testing.T) {
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	// Set up external IPAM
	js, err := nc.JetStream()
	require.NoError(t, err)
	ipamKV, err := utils.GetOrCreateKVBucket(js, KVBucketExternalIPAM, 5)
	require.NoError(t, err)
	pools := []ExternalPoolConfig{{
		Name:       "test-pool",
		Source:     "static",
		RangeStart: "203.0.113.10",
		RangeEnd:   "203.0.113.20",
		Gateway:    "203.0.113.1",
		PrefixLen:  24,
		Region:     "us-east-1",
		AZ:         "us-east-1a",
	}}
	extIPAM := NewExternalIPAMWithKV(ipamKV, pools)
	require.NoError(t, extIPAM.initPools())

	// Allocate and assign public IP to the ENI
	publicIP, poolName, err := extIPAM.AllocateIP("us-east-1", "us-east-1a", "auto_assign", eniId, "i-test")
	require.NoError(t, err)
	require.NoError(t, svc.UpdateENIPublicIP(testAccountID, eniId, publicIP, poolName))

	// Create an EIP record that references this ENI
	eipKV, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "spinifex-vpc-elastic-ips", History: 1})
	require.NoError(t, err)
	eipRecord, _ := json.Marshal(struct {
		AllocationId string `json:"allocation_id"`
		PublicIp     string `json:"public_ip"`
		ENIId        string `json:"eni_id"`
		State        string `json:"state"`
	}{
		AllocationId: "eipalloc-test1",
		PublicIp:     publicIP,
		ENIId:        eniId,
		State:        "associated",
	})
	_, err = eipKV.Put(utils.AccountKey(testAccountID, "eipalloc-test1"), eipRecord)
	require.NoError(t, err)

	// Inject external IPAM WITH EIP KV
	svc.SetExternalIPAM(extIPAM, eipKV)

	// Subscribe to vpc.delete-nat — should NOT receive an event
	natCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete-nat", func(msg *nats.Msg) {
		natCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	// Delete the ENI
	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	require.NoError(t, err)

	// Verify NO vpc.delete-nat event was published (EIP-owned, should be skipped)
	select {
	case <-natCh:
		t.Fatal("unexpected vpc.delete-nat event — EIP-owned public IP should not be released")
	case <-time.After(500 * time.Millisecond):
		// Expected: no event
	}
}

func TestDeleteNetworkInterface_NoPublicIP_NoExternalIPAM(t *testing.T) {
	// Verify that delete still works when no external IPAM is configured
	// (the common case — no public IP on the ENI)
	svc, nc := setupTestVPCServiceWithNC(t)
	vpcId := createTestVPC(t, svc, "10.0.0.0/16")
	subnetId := createTestSubnet(t, svc, vpcId, "10.0.1.0/24")
	eniId := createTestENI(t, svc, subnetId)

	// No SetExternalIPAM call — externalIPAM is nil

	// Subscribe to vpc.delete-nat — should NOT receive an event
	natCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.delete-nat", func(msg *nats.Msg) {
		natCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	// Delete should succeed
	_, err = svc.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(eniId),
	}, testAccountID)
	require.NoError(t, err)

	// Verify no NAT event
	select {
	case <-natCh:
		t.Fatal("unexpected vpc.delete-nat event when no external IPAM is configured")
	case <-time.After(500 * time.Millisecond):
		// Expected
	}

	// Verify ENI is deleted
	_, err = svc.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniId)},
	}, testAccountID)
	assert.ErrorContains(t, err, "InvalidNetworkInterfaceID.NotFound")
}

func TestMatchFilter(t *testing.T) {
	// Exact match
	assert.True(t, matchFilter("hello", "hello"))
	assert.False(t, matchFilter("hello", "world"))

	// Wildcard only
	assert.True(t, matchFilter("anything", "*"))

	// Trailing wildcard (AWS-style prefix match)
	assert.True(t, matchFilter("ELB app/my-alb/lb-123", "ELB *"))
	assert.True(t, matchFilter("ELB something", "ELB *"))
	assert.False(t, matchFilter("NOT ELB", "ELB *"))

	// Leading wildcard
	assert.True(t, matchFilter("my-alb-123", "*123"))
	assert.False(t, matchFilter("my-alb-456", "*123"))

	// Middle wildcard
	assert.True(t, matchFilter("ELB app/test/lb-1", "ELB */lb-1"))
	assert.False(t, matchFilter("ELB app/test/lb-2", "ELB */lb-1"))

	// No wildcard = exact
	assert.True(t, matchFilter("exact", "exact"))
	assert.False(t, matchFilter("exact!", "exact"))
}
