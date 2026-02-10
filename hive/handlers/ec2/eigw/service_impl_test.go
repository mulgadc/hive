package handlers_ec2_eigw

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

var _ EgressOnlyIGWService = (*EgressOnlyIGWServiceImpl)(nil)

func setupTestEIGWService(t *testing.T) *EgressOnlyIGWServiceImpl {
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

	svc, err := NewEgressOnlyIGWServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

func createTestEIGW(t *testing.T, svc *EgressOnlyIGWServiceImpl) string {
	t.Helper()
	out, err := svc.CreateEgressOnlyInternetGateway(&ec2.CreateEgressOnlyInternetGatewayInput{
		VpcId: aws.String("vpc-test123"),
	})
	require.NoError(t, err)
	return *out.EgressOnlyInternetGateway.EgressOnlyInternetGatewayId
}

func TestCreateEgressOnlyInternetGateway(t *testing.T) {
	svc := setupTestEIGWService(t)
	out, err := svc.CreateEgressOnlyInternetGateway(&ec2.CreateEgressOnlyInternetGatewayInput{
		VpcId: aws.String("vpc-test123"),
	})
	require.NoError(t, err)
	require.NotNil(t, out.EgressOnlyInternetGateway)
	assert.Equal(t, "eigw-", (*out.EgressOnlyInternetGateway.EgressOnlyInternetGatewayId)[:5])
	require.NotEmpty(t, out.EgressOnlyInternetGateway.Attachments)
	assert.Equal(t, "vpc-test123", *out.EgressOnlyInternetGateway.Attachments[0].VpcId)
	assert.Equal(t, "attached", *out.EgressOnlyInternetGateway.Attachments[0].State)
}

func TestCreateEgressOnlyInternetGateway_MissingVpcId(t *testing.T) {
	svc := setupTestEIGWService(t)
	_, err := svc.CreateEgressOnlyInternetGateway(&ec2.CreateEgressOnlyInternetGatewayInput{})
	assert.Error(t, err)
}

func TestDeleteEgressOnlyInternetGateway(t *testing.T) {
	svc := setupTestEIGWService(t)
	eigwID := createTestEIGW(t, svc)

	out, err := svc.DeleteEgressOnlyInternetGateway(&ec2.DeleteEgressOnlyInternetGatewayInput{
		EgressOnlyInternetGatewayId: aws.String(eigwID),
	})
	require.NoError(t, err)
	assert.True(t, *out.ReturnCode)

	desc, err := svc.DescribeEgressOnlyInternetGateways(&ec2.DescribeEgressOnlyInternetGatewaysInput{
		EgressOnlyInternetGatewayIds: []*string{aws.String(eigwID)},
	})
	require.NoError(t, err)
	assert.Empty(t, desc.EgressOnlyInternetGateways)
}

func TestDeleteEgressOnlyInternetGateway_NotFound(t *testing.T) {
	svc := setupTestEIGWService(t)
	_, err := svc.DeleteEgressOnlyInternetGateway(&ec2.DeleteEgressOnlyInternetGatewayInput{
		EgressOnlyInternetGatewayId: aws.String("eigw-nonexistent"),
	})
	assert.Error(t, err)
}

func TestDeleteEgressOnlyInternetGateway_MissingID(t *testing.T) {
	svc := setupTestEIGWService(t)
	_, err := svc.DeleteEgressOnlyInternetGateway(&ec2.DeleteEgressOnlyInternetGatewayInput{})
	assert.Error(t, err)
}

func TestDescribeEgressOnlyInternetGateways_All(t *testing.T) {
	svc := setupTestEIGWService(t)
	createTestEIGW(t, svc)
	createTestEIGW(t, svc)

	desc, err := svc.DescribeEgressOnlyInternetGateways(&ec2.DescribeEgressOnlyInternetGatewaysInput{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.EgressOnlyInternetGateways), 2)
}

func TestDescribeEgressOnlyInternetGateways_ByID(t *testing.T) {
	svc := setupTestEIGWService(t)
	eigwID := createTestEIGW(t, svc)

	desc, err := svc.DescribeEgressOnlyInternetGateways(&ec2.DescribeEgressOnlyInternetGatewaysInput{
		EgressOnlyInternetGatewayIds: []*string{aws.String(eigwID)},
	})
	require.NoError(t, err)
	require.Len(t, desc.EgressOnlyInternetGateways, 1)
	assert.Equal(t, eigwID, *desc.EgressOnlyInternetGateways[0].EgressOnlyInternetGatewayId)
}

func TestDescribeEgressOnlyInternetGateways_Empty(t *testing.T) {
	svc := setupTestEIGWService(t)
	desc, err := svc.DescribeEgressOnlyInternetGateways(&ec2.DescribeEgressOnlyInternetGatewaysInput{})
	require.NoError(t, err)
	assert.Empty(t, desc.EgressOnlyInternetGateways)
}
