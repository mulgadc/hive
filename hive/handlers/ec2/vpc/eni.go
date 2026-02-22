package handlers_ec2_vpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

const (
	KVBucketENIs = "hive-vpc-enis"
)

// ENIRecord represents a stored Elastic Network Interface
type ENIRecord struct {
	NetworkInterfaceId string            `json:"network_interface_id"`
	SubnetId           string            `json:"subnet_id"`
	VpcId              string            `json:"vpc_id"`
	AvailabilityZone   string            `json:"availability_zone"`
	PrivateIpAddress   string            `json:"private_ip_address"`
	MacAddress         string            `json:"mac_address"`
	Description        string            `json:"description"`
	Status             string            `json:"status"` // available, in-use, attaching, detaching
	AttachmentId       string            `json:"attachment_id,omitempty"`
	InstanceId         string            `json:"instance_id,omitempty"`
	DeviceIndex        int64             `json:"device_index"`
	Tags               map[string]string `json:"tags"`
	CreatedAt          time.Time         `json:"created_at"`
}

// CreateNetworkInterface creates a new ENI in the specified subnet
func (s *VPCServiceImpl) CreateNetworkInterface(input *ec2.CreateNetworkInterfaceInput) (*ec2.CreateNetworkInterfaceOutput, error) {
	if err := s.ensureKVReady(); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if input.SubnetId == nil || *input.SubnetId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	subnetId := *input.SubnetId

	// Verify subnet exists and get its details
	subnetEntry, err := s.subnetKV.Get(subnetId)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidSubnetIDNotFound)
	}

	var subnet SubnetRecord
	if err := json.Unmarshal(subnetEntry.Value(), &subnet); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Allocate IP from subnet
	var privateIP string
	if input.PrivateIpAddress != nil && *input.PrivateIpAddress != "" {
		// TODO: validate the requested IP is in the subnet range and not already allocated
		privateIP = *input.PrivateIpAddress
	} else {
		ip, err := s.ipam.AllocateIP(subnetId, subnet.CidrBlock)
		if err != nil {
			return nil, errors.New(awserrors.ErrorServerInternal)
		}
		privateIP = ip
	}

	eniId := utils.GenerateResourceID("eni")

	// Generate a deterministic MAC address
	macAddr := generateENIMac(eniId)

	description := ""
	if input.Description != nil {
		description = *input.Description
	}

	record := ENIRecord{
		NetworkInterfaceId: eniId,
		SubnetId:           subnetId,
		VpcId:              subnet.VpcId,
		AvailabilityZone:   subnet.AvailabilityZone,
		PrivateIpAddress:   privateIP,
		MacAddress:         macAddr,
		Description:        description,
		Status:             "available",
		Tags:               make(map[string]string),
		CreatedAt:          time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "network-interface" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ENI record: %w", err)
	}
	if _, err := s.eniKV.Put(eniId, data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateNetworkInterface completed", "eniId", eniId, "subnetId", subnetId, "ip", privateIP)

	// Publish vpc.create-port event for vpcd topology translation
	s.publishENIEvent("vpc.create-port", eniId, subnetId, subnet.VpcId, privateIP, macAddr)

	return &ec2.CreateNetworkInterfaceOutput{
		NetworkInterface: s.eniRecordToEC2(&record),
	}, nil
}

// DeleteNetworkInterface deletes an ENI
func (s *VPCServiceImpl) DeleteNetworkInterface(input *ec2.DeleteNetworkInterfaceInput) (*ec2.DeleteNetworkInterfaceOutput, error) {
	if err := s.ensureKVReady(); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if input.NetworkInterfaceId == nil || *input.NetworkInterfaceId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	eniId := *input.NetworkInterfaceId

	// Get the ENI record
	entry, err := s.eniKV.Get(eniId)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidNetworkInterfaceIDNotFound)
	}

	var record ENIRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Cannot delete an in-use ENI
	if record.Status == "in-use" {
		return nil, errors.New(awserrors.ErrorInvalidNetworkInterfaceInUse)
	}

	// Release the IP back to the IPAM pool
	if err := s.ipam.ReleaseIP(record.SubnetId, record.PrivateIpAddress); err != nil {
		slog.Warn("Failed to release IP during ENI delete", "eni", eniId, "ip", record.PrivateIpAddress, "err", err)
	}

	if err := s.eniKV.Delete(eniId); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteNetworkInterface completed", "eniId", eniId)

	// Publish vpc.delete-port event for vpcd topology cleanup
	s.publishENIEvent("vpc.delete-port", eniId, record.SubnetId, record.VpcId, record.PrivateIpAddress, record.MacAddress)

	return &ec2.DeleteNetworkInterfaceOutput{}, nil
}

// DescribeNetworkInterfaces lists ENIs with optional filters
func (s *VPCServiceImpl) DescribeNetworkInterfaces(input *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
	if err := s.ensureKVReady(); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	var enis []*ec2.NetworkInterface

	eniIDs := make(map[string]bool)
	for _, id := range input.NetworkInterfaceIds {
		if id != nil {
			eniIDs[*id] = true
		}
	}

	// Extract filters
	subnetFilter := ""
	vpcFilter := ""
	attachmentInstanceFilter := ""
	for _, f := range input.Filters {
		if f.Name == nil || len(f.Values) == 0 || f.Values[0] == nil {
			continue
		}
		switch *f.Name {
		case "subnet-id":
			subnetFilter = *f.Values[0]
		case "vpc-id":
			vpcFilter = *f.Values[0]
		case "attachment.instance-id":
			attachmentInstanceFilter = *f.Values[0]
		}
	}

	keys, err := s.eniKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if len(eniIDs) > 0 && !eniIDs[key] {
			continue
		}

		entry, err := s.eniKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get ENI record", "key", key, "error", err)
			continue
		}

		var record ENIRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal ENI record", "key", key, "error", err)
			continue
		}

		if subnetFilter != "" && record.SubnetId != subnetFilter {
			continue
		}
		if vpcFilter != "" && record.VpcId != vpcFilter {
			continue
		}
		if attachmentInstanceFilter != "" && record.InstanceId != attachmentInstanceFilter {
			continue
		}

		enis = append(enis, s.eniRecordToEC2(&record))
	}

	// If specific ENI IDs were requested but not found, return error
	if len(eniIDs) > 0 {
		found := make(map[string]bool)
		for _, eni := range enis {
			if eni.NetworkInterfaceId != nil {
				found[*eni.NetworkInterfaceId] = true
			}
		}
		for id := range eniIDs {
			if !found[id] {
				return nil, errors.New(awserrors.ErrorInvalidNetworkInterfaceIDNotFound)
			}
		}
	}

	slog.Info("DescribeNetworkInterfaces completed", "count", len(enis))

	return &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: enis,
	}, nil
}

// AttachENI marks an ENI as attached to an instance (internal use by RunInstances)
func (s *VPCServiceImpl) AttachENI(eniId, instanceId string, deviceIndex int64) (string, error) {
	if err := s.ensureKVReady(); err != nil {
		return "", errors.New(awserrors.ErrorServerInternal)
	}
	entry, err := s.eniKV.Get(eniId)
	if err != nil {
		return "", errors.New(awserrors.ErrorInvalidNetworkInterfaceIDNotFound)
	}

	var record ENIRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return "", errors.New(awserrors.ErrorServerInternal)
	}

	if record.Status == "in-use" {
		return "", errors.New(awserrors.ErrorInvalidNetworkInterfaceInUse)
	}

	attachmentId := utils.GenerateResourceID("eni-attach")
	record.Status = "in-use"
	record.AttachmentId = attachmentId
	record.InstanceId = instanceId
	record.DeviceIndex = deviceIndex

	data, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ENI record: %w", err)
	}
	if _, err := s.eniKV.Update(eniId, data, entry.Revision()); err != nil {
		return "", errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("ENI attached", "eniId", eniId, "instanceId", instanceId, "attachmentId", attachmentId)
	return attachmentId, nil
}

// DetachENI marks an ENI as detached from an instance (internal use by TerminateInstances)
func (s *VPCServiceImpl) DetachENI(eniId string) error {
	if err := s.ensureKVReady(); err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}
	entry, err := s.eniKV.Get(eniId)
	if err != nil {
		return errors.New(awserrors.ErrorInvalidNetworkInterfaceIDNotFound)
	}

	var record ENIRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}

	record.Status = "available"
	record.AttachmentId = ""
	record.InstanceId = ""
	record.DeviceIndex = 0

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal ENI record: %w", err)
	}
	if _, err := s.eniKV.Update(eniId, data, entry.Revision()); err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("ENI detached", "eniId", eniId)
	return nil
}

// eniRecordToEC2 converts an ENI record to an EC2 NetworkInterface
func (s *VPCServiceImpl) eniRecordToEC2(record *ENIRecord) *ec2.NetworkInterface {
	eni := &ec2.NetworkInterface{
		NetworkInterfaceId: aws.String(record.NetworkInterfaceId),
		SubnetId:           aws.String(record.SubnetId),
		VpcId:              aws.String(record.VpcId),
		AvailabilityZone:   aws.String(record.AvailabilityZone),
		PrivateIpAddress:   aws.String(record.PrivateIpAddress),
		MacAddress:         aws.String(record.MacAddress),
		Description:        aws.String(record.Description),
		Status:             aws.String(record.Status),
		OwnerId:            aws.String("123456789012"),
		InterfaceType:      aws.String("interface"),
		SourceDestCheck:    aws.Bool(true),
		PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
			{
				Primary:          aws.Bool(true),
				PrivateIpAddress: aws.String(record.PrivateIpAddress),
			},
		},
		Groups: []*ec2.GroupIdentifier{},
	}

	if record.AttachmentId != "" {
		eni.Attachment = &ec2.NetworkInterfaceAttachment{
			AttachmentId:        aws.String(record.AttachmentId),
			InstanceId:          aws.String(record.InstanceId),
			DeviceIndex:         aws.Int64(record.DeviceIndex),
			Status:              aws.String("attached"),
			DeleteOnTermination: aws.Bool(true),
		}
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		eni.TagSet = tags
	}

	return eni
}

// generateENIMac creates a locally-administered unicast MAC address from an ENI ID.
func generateENIMac(eniId string) string {
	h := uint32(0)
	for _, c := range eniId {
		h = h*31 + uint32(c)
	}
	return fmt.Sprintf("02:00:00:%02x:%02x:%02x", (h>>16)&0xff, (h>>8)&0xff, h&0xff)
}

// publishENIEvent publishes an ENI lifecycle event to NATS for vpcd consumption.
func (s *VPCServiceImpl) publishENIEvent(topic, eniId, subnetId, vpcId, privateIP, macAddr string) {
	if s.natsConn == nil {
		return
	}
	evt := struct {
		NetworkInterfaceId string `json:"network_interface_id"`
		SubnetId           string `json:"subnet_id"`
		VpcId              string `json:"vpc_id"`
		PrivateIpAddress   string `json:"private_ip_address"`
		MacAddress         string `json:"mac_address"`
	}{
		NetworkInterfaceId: eniId,
		SubnetId:           subnetId,
		VpcId:              vpcId,
		PrivateIpAddress:   privateIP,
		MacAddress:         macAddr,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("Failed to marshal ENI event", "topic", topic, "err", err)
		return
	}
	if err := s.natsConn.Publish(topic, data); err != nil {
		slog.Error("Failed to publish ENI event", "topic", topic, "err", err)
	}
}
