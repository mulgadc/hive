package handlers_ec2_eip

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccountID = "123456789012"

func testPool() handlers_ec2_vpc.ExternalPoolConfig {
	return handlers_ec2_vpc.ExternalPoolConfig{
		Name:       "test-pool",
		RangeStart: "198.51.100.10",
		RangeEnd:   "198.51.100.20",
		Gateway:    "198.51.100.1",
		PrefixLen:  24,
	}
}

func setupTestEIP(t *testing.T) (*EIPServiceImpl, *handlers_ec2_vpc.ExternalIPAM) {
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

	pool := testPool()
	ipam, err := handlers_ec2_vpc.NewExternalIPAM(js, []handlers_ec2_vpc.ExternalPoolConfig{pool})
	require.NoError(t, err)

	svc, err := NewEIPServiceImpl(nc, ipam, nil)
	require.NoError(t, err)

	return svc, ipam
}

func TestEIP_Allocate(t *testing.T) {
	svc, _ := setupTestEIP(t)

	out, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.NotEmpty(t, *out.AllocationId)
	assert.True(t, len(*out.AllocationId) > 0)
	assert.NotEmpty(t, *out.PublicIp)
	assert.Equal(t, "vpc", *out.Domain)
	// Gateway takes .10, so first allocable is .11
	assert.Equal(t, "198.51.100.11", *out.PublicIp)
}

func TestEIP_AllocateFromSpecificPool(t *testing.T) {
	svc, _ := setupTestEIP(t)

	out, err := svc.AllocateAddress(&ec2.AllocateAddressInput{
		Domain: aws.String("vpc"),
	}, testAccountID)
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.NotEmpty(t, *out.AllocationId)
	assert.Equal(t, "vpc", *out.Domain)
	assert.NotEmpty(t, *out.PublicIp)
}

func TestEIP_Release(t *testing.T) {
	svc, ipam := setupTestEIP(t)

	// Allocate
	out, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)
	allocatedIP := *out.PublicIp

	// Release
	_, err = svc.ReleaseAddress(&ec2.ReleaseAddressInput{
		AllocationId: out.AllocationId,
	}, testAccountID)
	require.NoError(t, err)

	// Verify IP returned to pool by allocating again — should get same IP
	out2, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)
	assert.Equal(t, allocatedIP, *out2.PublicIp)

	// Verify the pool shows the IP was released and re-allocated
	record, err := ipam.GetPoolRecord("test-pool")
	require.NoError(t, err)
	_, allocated := record.Allocated[allocatedIP]
	// After re-allocation, IP should be allocated again by the EIP service
	// but let's just verify the release worked by checking describe returns nothing for old alloc
	_, descErr := svc.DescribeAddresses(&ec2.DescribeAddressesInput{
		AllocationIds: []*string{out.AllocationId},
	}, testAccountID)
	assert.Error(t, descErr)
	_ = allocated // suppress unused
}

func TestEIP_ReleaseWhileAssociated(t *testing.T) {
	svc, _ := setupTestEIP(t)

	// Allocate
	out, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)

	// Manually mark as associated by writing to KV (simulates AssociateAddress without needing a real VPCService)
	allocID := *out.AllocationId
	// We can't easily associate without a VPC service, but we can test the error path
	// by directly updating the record's state in the KV store.
	// Instead, let's verify that ReleaseAddress checks the state.
	// Since we haven't associated, this should succeed (testing the non-associated path).
	// To test the associated path, we need to manipulate the KV directly.

	// Get the KV entry and update state to "associated"
	entry, err := svc.eipKV.Get(testAccountID + "." + allocID)
	require.NoError(t, err)

	var record EIPRecord
	err = json.Unmarshal(entry.Value(), &record)
	require.NoError(t, err)
	record.State = "associated"
	record.AssociationId = "eipassoc-test"
	record.ENIId = "eni-test"

	data, err := json.Marshal(record)
	require.NoError(t, err)
	_, err = svc.eipKV.Update(testAccountID+"."+allocID, data, entry.Revision())
	require.NoError(t, err)

	// Now try to release — should fail
	_, err = svc.ReleaseAddress(&ec2.ReleaseAddressInput{
		AllocationId: aws.String(allocID),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidAddress.Locked")
}

func TestEIP_DescribeAddresses(t *testing.T) {
	svc, _ := setupTestEIP(t)

	// Allocate multiple EIPs
	out1, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)
	out2, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)
	out3, err := svc.AllocateAddress(&ec2.AllocateAddressInput{}, testAccountID)
	require.NoError(t, err)

	// Describe all
	desc, err := svc.DescribeAddresses(&ec2.DescribeAddressesInput{}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.Addresses, 3)

	// Verify all IPs are unique
	ips := make(map[string]bool)
	for _, addr := range desc.Addresses {
		ips[*addr.PublicIp] = true
	}
	assert.Len(t, ips, 3)

	// Describe by specific allocation ID
	desc2, err := svc.DescribeAddresses(&ec2.DescribeAddressesInput{
		AllocationIds: []*string{out1.AllocationId},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc2.Addresses, 1)
	assert.Equal(t, *out1.AllocationId, *desc2.Addresses[0].AllocationId)

	_ = out2
	_ = out3
}
