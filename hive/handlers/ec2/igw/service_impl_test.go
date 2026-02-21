package handlers_ec2_igw

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

func setupTestIGWService(t *testing.T) (*IGWServiceImpl, *nats.Conn) {
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

	svc, err := NewIGWServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc, nc
}

func createTestIGW(t *testing.T, svc *IGWServiceImpl) string {
	t.Helper()
	out, err := svc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{})
	require.NoError(t, err)
	return *out.InternetGateway.InternetGatewayId
}

func TestCreateInternetGateway(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	out, err := svc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{})
	require.NoError(t, err)
	require.NotNil(t, out.InternetGateway)
	assert.Equal(t, "igw-", (*out.InternetGateway.InternetGatewayId)[:4])
	// Should not have attachments when created
	assert.Empty(t, out.InternetGateway.Attachments)
}

func TestCreateInternetGateway_WithTags(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	out, err := svc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("internet-gateway"),
				Tags: []*ec2.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-igw")},
					{Key: aws.String("Env"), Value: aws.String("test")},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, out.InternetGateway.Tags, 2)

	// Verify tags persist through describe
	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{out.InternetGateway.InternetGatewayId},
	})
	require.NoError(t, err)
	require.Len(t, desc.InternetGateways, 1)
	assert.Len(t, desc.InternetGateways[0].Tags, 2)
}

func TestCreateInternetGateway_TagsWrongResourceType(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	out, err := svc.CreateInternetGateway(&ec2.CreateInternetGatewayInput{
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
	assert.Empty(t, out.InternetGateway.Tags)
}

func TestDeleteInternetGateway(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	_, err := svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	require.NoError(t, err)

	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{aws.String(igwID)},
	})
	require.NoError(t, err)
	assert.Empty(t, desc.InternetGateways)
}

func TestDeleteInternetGateway_NotFound(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String("igw-nonexistent"),
	})
	assert.ErrorContains(t, err, "InvalidInternetGatewayID.NotFound")
}

func TestDeleteInternetGateway_MissingID(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestDeleteInternetGateway_EmptyID(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(""),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestDeleteInternetGateway_WhileAttached(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	// Attach to a VPC
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Try to delete — should fail with DependencyViolation
	_, err = svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	assert.ErrorContains(t, err, "DependencyViolation")
}

func TestDescribeInternetGateways_All(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	createTestIGW(t, svc)
	createTestIGW(t, svc)

	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{})
	require.NoError(t, err)
	assert.Len(t, desc.InternetGateways, 2)
}

func TestDescribeInternetGateways_ByID(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)
	createTestIGW(t, svc) // second one should be filtered out

	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{aws.String(igwID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.InternetGateways, 1)
	assert.Equal(t, igwID, *desc.InternetGateways[0].InternetGatewayId)
}

func TestDescribeInternetGateways_Empty(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{})
	require.NoError(t, err)
	assert.Empty(t, desc.InternetGateways)
}

func TestAttachInternetGateway(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Verify attachment via describe
	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{aws.String(igwID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.InternetGateways, 1)
	require.Len(t, desc.InternetGateways[0].Attachments, 1)
	assert.Equal(t, "vpc-test123", *desc.InternetGateways[0].Attachments[0].VpcId)
	assert.Equal(t, "attached", *desc.InternetGateways[0].Attachments[0].State)
}

func TestAttachInternetGateway_NotFound(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-nonexistent"),
		VpcId:             aws.String("vpc-test123"),
	})
	assert.ErrorContains(t, err, "InvalidInternetGatewayID.NotFound")
}

func TestAttachInternetGateway_AlreadyAttached(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Try attaching again — should fail
	_, err = svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-other"),
	})
	assert.ErrorContains(t, err, "Resource.AlreadyAssociated")
}

func TestAttachInternetGateway_MissingParams(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		VpcId: aws.String("vpc-test123"),
	})
	assert.ErrorContains(t, err, "MissingParameter")

	_, err = svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-test"),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestDetachInternetGateway(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	// Attach first
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Detach
	_, err = svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Verify detached
	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		InternetGatewayIds: []*string{aws.String(igwID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.InternetGateways, 1)
	assert.Empty(t, desc.InternetGateways[0].Attachments)
}

func TestDetachInternetGateway_NotAttached(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	_, err := svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	assert.ErrorContains(t, err, "Gateway.NotAttached")
}

func TestDetachInternetGateway_WrongVPC(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-test123"),
	})
	require.NoError(t, err)

	// Try detaching from wrong VPC
	_, err = svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-wrong"),
	})
	assert.ErrorContains(t, err, "Gateway.NotAttached")
}

func TestDetachInternetGateway_NotFound(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-nonexistent"),
		VpcId:             aws.String("vpc-test123"),
	})
	assert.ErrorContains(t, err, "InvalidInternetGatewayID.NotFound")
}

func TestDetachInternetGateway_MissingParams(t *testing.T) {
	svc, _ := setupTestIGWService(t)
	_, err := svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		VpcId: aws.String("vpc-test123"),
	})
	assert.ErrorContains(t, err, "MissingParameter")

	_, err = svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String("igw-test"),
	})
	assert.ErrorContains(t, err, "MissingParameter")
}

func TestIGWLifecycle_CreateAttachDetachDelete(t *testing.T) {
	svc, _ := setupTestIGWService(t)

	// Create
	igwID := createTestIGW(t, svc)

	// Attach
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-lifecycle"),
	})
	require.NoError(t, err)

	// Cannot delete while attached
	_, err = svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	assert.ErrorContains(t, err, "DependencyViolation")

	// Detach
	_, err = svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-lifecycle"),
	})
	require.NoError(t, err)

	// Now delete succeeds
	_, err = svc.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
	})
	require.NoError(t, err)

	// Verify gone
	desc, err := svc.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{})
	require.NoError(t, err)
	assert.Empty(t, desc.InternetGateways)
}

func TestAttachInternetGateway_PublishesEvent(t *testing.T) {
	svc, nc := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	// Subscribe to IGW attach events
	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.igw-attach", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	_, err = svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-event-test"),
	})
	require.NoError(t, err)

	// Verify event was published
	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), igwID)
		assert.Contains(t, string(msg.Data), "vpc-event-test")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for IGW attach event")
	}
}

func TestDetachInternetGateway_PublishesEvent(t *testing.T) {
	svc, nc := setupTestIGWService(t)
	igwID := createTestIGW(t, svc)

	// Attach first
	_, err := svc.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-event-test"),
	})
	require.NoError(t, err)

	// Subscribe to IGW detach events
	eventCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("vpc.igw-detach", func(msg *nats.Msg) {
		eventCh <- msg
	})
	require.NoError(t, err)
	defer func() { _ = sub.Unsubscribe() }()

	_, err = svc.DetachInternetGateway(&ec2.DetachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String("vpc-event-test"),
	})
	require.NoError(t, err)

	// Verify event was published
	select {
	case msg := <-eventCh:
		assert.Contains(t, string(msg.Data), igwID)
		assert.Contains(t, string(msg.Data), "vpc-event-test")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for IGW detach event")
	}
}
