package handlers_ec2_eip

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// Ensure EIPServiceImpl implements EIPService.
var _ EIPService = (*EIPServiceImpl)(nil)

// EIPServiceImpl implements Elastic IP operations with NATS JetStream persistence.
type EIPServiceImpl struct {
	eipKV        nats.KeyValue
	externalIPAM *handlers_ec2_vpc.ExternalIPAM
	vpcService   handlers_ec2_vpc.VPCService
	natsConn     *nats.Conn
}

// natEvent is the payload published to vpc.add-nat / vpc.delete-nat topics.
type natEvent struct {
	VpcId      string `json:"vpc_id"`
	ExternalIP string `json:"external_ip"`
	LogicalIP  string `json:"logical_ip"`
	PortName   string `json:"port_name"`
	MAC        string `json:"mac"`
}

// NewEIPServiceImpl creates a new EIP service backed by NATS JetStream KV.
func NewEIPServiceImpl(natsConn *nats.Conn, externalIPAM *handlers_ec2_vpc.ExternalIPAM, vpcService handlers_ec2_vpc.VPCService) (*EIPServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	eipKV, err := utils.GetOrCreateKVBucket(js, KVBucketEIPs, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketEIPs, err)
	}
	if err := utils.WriteVersion(eipKV, KVBucketEIPsVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketEIPs, err)
	}

	slog.Info("EIP service initialized with JetStream KV", "bucket", KVBucketEIPs)

	return &EIPServiceImpl{
		eipKV:        eipKV,
		externalIPAM: externalIPAM,
		vpcService:   vpcService,
		natsConn:     natsConn,
	}, nil
}

// AllocateAddress allocates a new Elastic IP from the external IPAM pool.
func (s *EIPServiceImpl) AllocateAddress(input *ec2.AllocateAddressInput, accountID string) (*ec2.AllocateAddressOutput, error) {
	allocID := utils.GenerateResourceID("eipalloc")

	var publicIP, poolName string
	var err error

	if input.PublicIpv4Pool != nil && *input.PublicIpv4Pool != "" {
		// Allocate from a specific named pool.
		poolName = *input.PublicIpv4Pool
		publicIP, err = s.externalIPAM.AllocateFromPool(poolName, "elastic_ip", "", "")
		if err != nil {
			slog.Error("AllocateAddress: IPAM pool allocation failed", "pool", poolName, "err", err)
			return nil, errors.New(awserrors.ErrorInsufficientAddressCapacity)
		}
	} else {
		// Allocate from the best pool matching region/AZ (empty strings = global fallback).
		region := ""
		az := ""
		publicIP, poolName, err = s.externalIPAM.AllocateIP(region, az, "elastic_ip", "", "")
		if err != nil {
			slog.Error("AllocateAddress: IPAM allocation failed", "err", err)
			return nil, errors.New(awserrors.ErrorInsufficientAddressCapacity)
		}
	}

	record := EIPRecord{
		AllocationId: allocID,
		PublicIp:     publicIP,
		PoolName:     poolName,
		State:        "allocated",
		Tags:         make(map[string]string),
		CreatedAt:    time.Now(),
	}

	// Store tags from input.
	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "elastic-ip" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EIP record: %w", err)
	}
	if _, err := s.eipKV.Put(utils.AccountKey(accountID, allocID), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("AllocateAddress completed", "allocationId", allocID, "publicIp", publicIP, "pool", poolName, "accountID", accountID)

	return &ec2.AllocateAddressOutput{
		AllocationId:   aws.String(allocID),
		PublicIp:       aws.String(publicIP),
		Domain:         aws.String("vpc"),
		PublicIpv4Pool: aws.String(poolName),
	}, nil
}

// ReleaseAddress releases a previously allocated Elastic IP back to the IPAM pool.
func (s *EIPServiceImpl) ReleaseAddress(input *ec2.ReleaseAddressInput, accountID string) (*ec2.ReleaseAddressOutput, error) {
	if input.AllocationId == nil || *input.AllocationId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	allocID := *input.AllocationId
	key := utils.AccountKey(accountID, allocID)

	entry, err := s.eipKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidAllocationIDNotFound)
	}

	var record EIPRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Cannot release an EIP that is still associated.
	if record.State == "associated" {
		return nil, errors.New(awserrors.ErrorInvalidAddressLocked)
	}

	// Release IP back to IPAM pool.
	if err := s.externalIPAM.ReleaseIP(record.PoolName, record.PublicIp); err != nil {
		slog.Warn("Failed to release IP back to IPAM pool", "allocationId", allocID, "ip", record.PublicIp, "pool", record.PoolName, "err", err)
	}

	if err := s.eipKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("ReleaseAddress completed", "allocationId", allocID, "publicIp", record.PublicIp, "accountID", accountID)

	return &ec2.ReleaseAddressOutput{}, nil
}

// AssociateAddress associates an Elastic IP with an ENI or instance.
func (s *EIPServiceImpl) AssociateAddress(input *ec2.AssociateAddressInput, accountID string) (*ec2.AssociateAddressOutput, error) {
	if input.AllocationId == nil || *input.AllocationId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	allocID := *input.AllocationId
	key := utils.AccountKey(accountID, allocID)

	entry, err := s.eipKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidAllocationIDNotFound)
	}

	var record EIPRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Resolve the target ENI. Either by direct NetworkInterfaceId or by InstanceId lookup.
	var eniID, instanceID, privateIP, vpcID, macAddr string

	if input.NetworkInterfaceId != nil && *input.NetworkInterfaceId != "" {
		eniID = *input.NetworkInterfaceId
		eni, err := s.lookupENI(accountID, eniID)
		if err != nil {
			return nil, err
		}
		instanceID = eni.InstanceId
		privateIP = eni.PrivateIpAddress
		vpcID = eni.VpcId
		macAddr = eni.MacAddress
	} else if input.InstanceId != nil && *input.InstanceId != "" {
		instanceID = *input.InstanceId
		eni, err := s.lookupENIByInstance(accountID, instanceID)
		if err != nil {
			return nil, err
		}
		eniID = eni.NetworkInterfaceId
		privateIP = eni.PrivateIpAddress
		vpcID = eni.VpcId
		macAddr = eni.MacAddress
	} else {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	// Allow override of private IP if specified.
	if input.PrivateIpAddress != nil && *input.PrivateIpAddress != "" {
		privateIP = *input.PrivateIpAddress
	}

	associationID := utils.GenerateResourceID("eipassoc")

	record.AssociationId = associationID
	record.ENIId = eniID
	record.InstanceId = instanceID
	record.PrivateIp = privateIP
	record.VpcId = vpcID
	record.State = "associated"

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EIP record: %w", err)
	}
	if _, err := s.eipKV.Update(key, data, entry.Revision()); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Publish vpc.add-nat event (fire-and-forget).
	s.publishNATEvent("vpc.add-nat", vpcID, record.PublicIp, privateIP, eniID, macAddr)

	slog.Info("AssociateAddress completed",
		"allocationId", allocID,
		"associationId", associationID,
		"eniId", eniID,
		"instanceId", instanceID,
		"publicIp", record.PublicIp,
		"privateIp", privateIP,
		"accountID", accountID)

	return &ec2.AssociateAddressOutput{
		AssociationId: aws.String(associationID),
	}, nil
}

// DisassociateAddress removes an Elastic IP association from an ENI.
func (s *EIPServiceImpl) DisassociateAddress(input *ec2.DisassociateAddressInput, accountID string) (*ec2.DisassociateAddressOutput, error) {
	if input.AssociationId == nil || *input.AssociationId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	associationID := *input.AssociationId

	// Find the EIP record by association ID.
	record, key, revision, err := s.findByAssociationID(accountID, associationID)
	if err != nil {
		return nil, err
	}

	// Publish vpc.delete-nat event before clearing association (fire-and-forget).
	if record.ENIId != "" {
		eni, lookupErr := s.lookupENI(accountID, record.ENIId)
		macAddr := ""
		if lookupErr == nil {
			macAddr = eni.MacAddress
		}
		s.publishNATEvent("vpc.delete-nat", record.VpcId, record.PublicIp, record.PrivateIp, record.ENIId, macAddr)
	}

	// Clear association fields, revert to "allocated" state.
	record.AssociationId = ""
	record.ENIId = ""
	record.InstanceId = ""
	record.PrivateIp = ""
	record.VpcId = ""
	record.State = "allocated"

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal EIP record: %w", err)
	}
	if _, err := s.eipKV.Update(key, data, revision); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DisassociateAddress completed", "associationId", associationID, "accountID", accountID)

	return &ec2.DisassociateAddressOutput{}, nil
}

// DescribeAddresses lists Elastic IPs with optional filtering by allocation ID.
func (s *EIPServiceImpl) DescribeAddresses(input *ec2.DescribeAddressesInput, accountID string) (*ec2.DescribeAddressesOutput, error) {
	allocIDs := make(map[string]bool)
	for _, id := range input.AllocationIds {
		if id != nil {
			allocIDs[*id] = true
		}
	}

	publicIPs := make(map[string]bool)
	for _, ip := range input.PublicIps {
		if ip != nil {
			publicIPs[*ip] = true
		}
	}

	prefix := accountID + "."
	keys, err := s.eipKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var addresses []*ec2.Address
	for _, k := range keys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		entry, err := s.eipKV.Get(k)
		if err != nil {
			slog.Warn("Failed to get EIP record", "key", k, "error", err)
			continue
		}

		var record EIPRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal EIP record", "key", k, "error", err)
			continue
		}

		if len(allocIDs) > 0 && !allocIDs[record.AllocationId] {
			continue
		}
		if len(publicIPs) > 0 && !publicIPs[record.PublicIp] {
			continue
		}

		addresses = append(addresses, s.eipRecordToEC2(&record))
	}

	// If specific allocation IDs were requested but not found, return error.
	if len(allocIDs) > 0 {
		found := make(map[string]bool)
		for _, addr := range addresses {
			if addr.AllocationId != nil {
				found[*addr.AllocationId] = true
			}
		}
		for id := range allocIDs {
			if !found[id] {
				return nil, errors.New(awserrors.ErrorInvalidAllocationIDNotFound)
			}
		}
	}

	slog.Info("DescribeAddresses completed", "count", len(addresses), "accountID", accountID)

	return &ec2.DescribeAddressesOutput{
		Addresses: addresses,
	}, nil
}

// lookupENI retrieves an ENI record by its ID using the VPC service.
func (s *EIPServiceImpl) lookupENI(accountID, eniID string) (*handlers_ec2_vpc.ENIRecord, error) {
	output, err := s.vpcService.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{aws.String(eniID)},
	}, accountID)
	if err != nil {
		return nil, err
	}
	if len(output.NetworkInterfaces) == 0 {
		return nil, errors.New(awserrors.ErrorInvalidNetworkInterfaceIDNotFound)
	}

	ni := output.NetworkInterfaces[0]
	record := &handlers_ec2_vpc.ENIRecord{
		NetworkInterfaceId: aws.StringValue(ni.NetworkInterfaceId),
		PrivateIpAddress:   aws.StringValue(ni.PrivateIpAddress),
		VpcId:              aws.StringValue(ni.VpcId),
		MacAddress:         aws.StringValue(ni.MacAddress),
		SubnetId:           aws.StringValue(ni.SubnetId),
	}
	if ni.Attachment != nil && ni.Attachment.InstanceId != nil {
		record.InstanceId = aws.StringValue(ni.Attachment.InstanceId)
	}
	return record, nil
}

// lookupENIByInstance finds the primary ENI for an instance.
func (s *EIPServiceImpl) lookupENIByInstance(accountID, instanceID string) (*handlers_ec2_vpc.ENIRecord, error) {
	output, err := s.vpcService.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("attachment.instance-id"),
				Values: []*string{aws.String(instanceID)},
			},
		},
	}, accountID)
	if err != nil {
		return nil, err
	}
	if len(output.NetworkInterfaces) == 0 {
		return nil, errors.New(awserrors.ErrorInvalidInstanceIDNotFound)
	}

	// Use the first ENI (primary).
	ni := output.NetworkInterfaces[0]
	record := &handlers_ec2_vpc.ENIRecord{
		NetworkInterfaceId: aws.StringValue(ni.NetworkInterfaceId),
		PrivateIpAddress:   aws.StringValue(ni.PrivateIpAddress),
		VpcId:              aws.StringValue(ni.VpcId),
		MacAddress:         aws.StringValue(ni.MacAddress),
		SubnetId:           aws.StringValue(ni.SubnetId),
	}
	if ni.Attachment != nil && ni.Attachment.InstanceId != nil {
		record.InstanceId = aws.StringValue(ni.Attachment.InstanceId)
	}
	return record, nil
}

// findByAssociationID scans EIP records to find one matching the given association ID.
func (s *EIPServiceImpl) findByAssociationID(accountID, associationID string) (*EIPRecord, string, uint64, error) {
	prefix := accountID + "."
	keys, err := s.eipKV.Keys()
	if err != nil {
		return nil, "", 0, errors.New(awserrors.ErrorServerInternal)
	}

	for _, k := range keys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		entry, err := s.eipKV.Get(k)
		if err != nil {
			continue
		}

		var record EIPRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}

		if record.AssociationId == associationID {
			return &record, k, entry.Revision(), nil
		}
	}

	return nil, "", 0, errors.New(awserrors.ErrorInvalidAssociationIDNotFound)
}

// publishNATEvent publishes a NAT lifecycle event to NATS for vpcd consumption.
// This is fire-and-forget; errors are logged but do not fail the API response.
func (s *EIPServiceImpl) publishNATEvent(topic, vpcID, externalIP, logicalIP, eniID, mac string) {
	if s.natsConn == nil {
		return
	}

	evt := natEvent{
		VpcId:      vpcID,
		ExternalIP: externalIP,
		LogicalIP:  logicalIP,
		PortName:   eniID,
		MAC:        mac,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("Failed to marshal NAT event", "topic", topic, "err", err)
		return
	}
	if err := s.natsConn.Publish(topic, data); err != nil {
		slog.Error("Failed to publish NAT event", "topic", topic, "err", err)
	}
}

// eipRecordToEC2 converts an EIPRecord to an EC2 Address.
func (s *EIPServiceImpl) eipRecordToEC2(record *EIPRecord) *ec2.Address {
	addr := &ec2.Address{
		AllocationId:   aws.String(record.AllocationId),
		PublicIp:       aws.String(record.PublicIp),
		Domain:         aws.String("vpc"),
		PublicIpv4Pool: aws.String(record.PoolName),
	}

	if record.AssociationId != "" {
		addr.AssociationId = aws.String(record.AssociationId)
	}
	if record.ENIId != "" {
		addr.NetworkInterfaceId = aws.String(record.ENIId)
	}
	if record.InstanceId != "" {
		addr.InstanceId = aws.String(record.InstanceId)
	}
	if record.PrivateIp != "" {
		addr.PrivateIpAddress = aws.String(record.PrivateIp)
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		addr.Tags = tags
	}

	return addr
}
