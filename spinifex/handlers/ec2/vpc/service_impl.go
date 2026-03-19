package handlers_ec2_vpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// Ensure VPCServiceImpl implements VPCService
var _ VPCService = (*VPCServiceImpl)(nil)

const (
	KVBucketVPCs       = "spinifex-vpc-vpcs"
	KVBucketSubnets    = "spinifex-vpc-subnets"
	KVBucketVNICounter = "spinifex-vpc-vni-counter"
	vniCounterKey      = "counter"
	vniStart           = 100 // Starting VNI value (avoid 0 and low numbers)

	KVBucketVPCsVersion       = 1
	KVBucketSubnetsVersion    = 1
	KVBucketVNICounterVersion = 1
)

// VPCRecord represents a stored VPC
type VPCRecord struct {
	VpcId     string            `json:"vpc_id"`
	CidrBlock string            `json:"cidr_block"`
	State     string            `json:"state"`
	IsDefault bool              `json:"is_default"`
	VNI       int64             `json:"vni"`
	Tags      map[string]string `json:"tags"`
	CreatedAt time.Time         `json:"created_at"`
}

// SubnetRecord represents a stored Subnet
type SubnetRecord struct {
	SubnetId         string            `json:"subnet_id"`
	VpcId            string            `json:"vpc_id"`
	CidrBlock        string            `json:"cidr_block"`
	AvailabilityZone string            `json:"availability_zone"`
	State            string            `json:"state"`
	IsDefault        bool              `json:"is_default"`
	Tags             map[string]string `json:"tags"`
	CreatedAt        time.Time         `json:"created_at"`
}

// VPCServiceImpl implements VPC, Subnet, and ENI operations with NATS JetStream persistence
type VPCServiceImpl struct {
	config   *config.Config
	natsConn *nats.Conn
	vpcKV    nats.KeyValue
	subnetKV nats.KeyValue
	vniKV    nats.KeyValue
	eniKV    nats.KeyValue
	ipam     *IPAM
}

// NewVPCServiceImplWithNATS creates a VPC service with NATS JetStream for persistence
func NewVPCServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*VPCServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	vpcKV, err := utils.GetOrCreateKVBucket(js, KVBucketVPCs, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketVPCs, err)
	}
	if err := utils.WriteVersion(vpcKV, KVBucketVPCsVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketVPCs, err)
	}

	subnetKV, err := utils.GetOrCreateKVBucket(js, KVBucketSubnets, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketSubnets, err)
	}
	if err := utils.WriteVersion(subnetKV, KVBucketSubnetsVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketSubnets, err)
	}

	vniKV, err := utils.GetOrCreateKVBucket(js, KVBucketVNICounter, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketVNICounter, err)
	}
	if err := utils.WriteVersion(vniKV, KVBucketVNICounterVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketVNICounter, err)
	}

	eniKV, err := utils.GetOrCreateKVBucket(js, KVBucketENIs, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketENIs, err)
	}
	if err := utils.WriteVersion(eniKV, KVBucketENIsVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketENIs, err)
	}

	ipam, err := NewIPAM(js)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IPAM: %w", err)
	}

	slog.Info("VPC service initialized with JetStream KV",
		"vpcBucket", KVBucketVPCs,
		"subnetBucket", KVBucketSubnets,
		"vniBucket", KVBucketVNICounter,
		"eniBucket", KVBucketENIs)

	return &VPCServiceImpl{
		config:   cfg,
		natsConn: natsConn,
		vpcKV:    vpcKV,
		subnetKV: subnetKV,
		vniKV:    vniKV,
		eniKV:    eniKV,
		ipam:     ipam,
	}, nil
}

// nextVNI allocates the next VNI using atomic increment on the NATS KV counter
func (s *VPCServiceImpl) nextVNI() (int64, error) {
	entry, err := s.vniKV.Get(vniCounterKey)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			// First VNI allocation — initialize counter
			vni := int64(vniStart)
			data, _ := json.Marshal(vni + 1)
			if _, err := s.vniKV.Create(vniCounterKey, data); err != nil {
				return 0, fmt.Errorf("failed to initialize VNI counter: %w", err)
			}
			return vni, nil
		}
		return 0, fmt.Errorf("failed to get VNI counter: %w", err)
	}

	var current int64
	if err := json.Unmarshal(entry.Value(), &current); err != nil {
		return 0, fmt.Errorf("failed to unmarshal VNI counter: %w", err)
	}

	next := current + 1
	data, _ := json.Marshal(next)
	if _, err := s.vniKV.Update(vniCounterKey, data, entry.Revision()); err != nil {
		return 0, fmt.Errorf("failed to update VNI counter (CAS conflict): %w", err)
	}

	return current, nil
}

// CreateVpc creates a new VPC
func (s *VPCServiceImpl) CreateVpc(input *ec2.CreateVpcInput, accountID string) (*ec2.CreateVpcOutput, error) {
	if input.CidrBlock == nil || *input.CidrBlock == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	// Validate CIDR block
	_, ipNet, err := net.ParseCIDR(*input.CidrBlock)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidVpcRange)
	}

	// AWS allows /16 to /28 for VPC CIDR blocks
	ones, _ := ipNet.Mask.Size()
	if ones < 16 || ones > 28 {
		return nil, errors.New(awserrors.ErrorInvalidVpcRange)
	}

	// Allocate VNI for overlay network
	vni, err := s.nextVNI()
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	vpcID := utils.GenerateResourceID("vpc")

	record := VPCRecord{
		VpcId:     vpcID,
		CidrBlock: ipNet.String(), // Normalize CIDR
		State:     "available",
		IsDefault: false,
		VNI:       vni,
		Tags:      make(map[string]string),
		CreatedAt: time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "vpc" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal VPC record: %w", err)
	}
	if _, err := s.vpcKV.Put(utils.AccountKey(accountID, vpcID), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateVpc completed", "vpcId", vpcID, "cidrBlock", record.CidrBlock, "vni", vni, "accountID", accountID)

	// Publish vpc.create event for vpcd topology translation
	s.publishVPCEvent("vpc.create", record.VpcId, record.CidrBlock, record.VNI)

	return &ec2.CreateVpcOutput{
		Vpc: s.vpcRecordToEC2(&record, accountID),
	}, nil
}

// DeleteVpc deletes a VPC
func (s *VPCServiceImpl) DeleteVpc(input *ec2.DeleteVpcInput, accountID string) (*ec2.DeleteVpcOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	vpcID := *input.VpcId
	key := utils.AccountKey(accountID, vpcID)

	if _, err := s.vpcKV.Get(key); err != nil {
		return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
	}

	// Check for dependent subnets owned by this account
	prefix := accountID + "."
	subnetKeys, err := s.subnetKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	for _, k := range subnetKeys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := s.subnetKV.Get(k)
		if err != nil {
			continue
		}
		var subnet SubnetRecord
		if err := json.Unmarshal(entry.Value(), &subnet); err != nil {
			continue
		}
		if subnet.VpcId == vpcID {
			return nil, errors.New(awserrors.ErrorDependencyViolation)
		}
	}

	if err := s.vpcKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteVpc completed", "vpcId", vpcID, "accountID", accountID)

	// Publish vpc.delete event for vpcd topology cleanup
	s.publishVPCEvent("vpc.delete", vpcID, "", 0)

	return &ec2.DeleteVpcOutput{}, nil
}

// DescribeVpcs describes VPCs
func (s *VPCServiceImpl) DescribeVpcs(input *ec2.DescribeVpcsInput, accountID string) (*ec2.DescribeVpcsOutput, error) {
	var vpcs []*ec2.Vpc

	vpcIDs := make(map[string]bool)
	for _, id := range input.VpcIds {
		if id != nil {
			vpcIDs[*id] = true
		}
	}

	prefix := accountID + "."
	keys, err := s.vpcKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.vpcKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get VPC record", "key", key, "error", err)
			continue
		}

		var record VPCRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal VPC record", "key", key, "error", err)
			continue
		}

		if len(vpcIDs) > 0 && !vpcIDs[record.VpcId] {
			continue
		}

		vpcs = append(vpcs, s.vpcRecordToEC2(&record, accountID))
	}

	// If specific VPC IDs were requested but not found, return error
	if len(vpcIDs) > 0 {
		found := make(map[string]bool)
		for _, vpc := range vpcs {
			if vpc.VpcId != nil {
				found[*vpc.VpcId] = true
			}
		}
		for id := range vpcIDs {
			if !found[id] {
				return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
			}
		}
	}

	slog.Info("DescribeVpcs completed", "count", len(vpcs), "accountID", accountID)

	return &ec2.DescribeVpcsOutput{
		Vpcs: vpcs,
	}, nil
}

// CreateSubnet creates a new subnet within a VPC
func (s *VPCServiceImpl) CreateSubnet(input *ec2.CreateSubnetInput, accountID string) (*ec2.CreateSubnetOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.CidrBlock == nil || *input.CidrBlock == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	vpcID := *input.VpcId

	// Verify VPC exists and belongs to this account
	vpcEntry, err := s.vpcKV.Get(utils.AccountKey(accountID, vpcID))
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
	}

	var vpcRecord VPCRecord
	if err := json.Unmarshal(vpcEntry.Value(), &vpcRecord); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Validate subnet CIDR
	_, subnetNet, err := net.ParseCIDR(*input.CidrBlock)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidSubnetRange)
	}

	// AWS allows /16 to /28 for subnet CIDR blocks
	ones, _ := subnetNet.Mask.Size()
	if ones < 16 || ones > 28 {
		return nil, errors.New(awserrors.ErrorInvalidSubnetRange)
	}

	// Verify subnet CIDR is within VPC CIDR
	_, vpcNet, err := net.ParseCIDR(vpcRecord.CidrBlock)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	if !vpcNet.Contains(subnetNet.IP) {
		return nil, errors.New(awserrors.ErrorInvalidSubnetRange)
	}

	// Check for CIDR conflicts with existing subnets in this VPC (same account)
	prefix := accountID + "."
	subnetKeys, err := s.subnetKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, k := range subnetKeys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := s.subnetKV.Get(k)
		if err != nil {
			continue
		}
		var existing SubnetRecord
		if err := json.Unmarshal(entry.Value(), &existing); err != nil {
			continue
		}
		if existing.VpcId != vpcID {
			continue
		}
		_, existingNet, err := net.ParseCIDR(existing.CidrBlock)
		if err != nil {
			continue
		}
		if existingNet.Contains(subnetNet.IP) || subnetNet.Contains(existingNet.IP) {
			return nil, errors.New(awserrors.ErrorInvalidSubnetConflict)
		}
	}

	// Determine AZ
	az := ""
	if input.AvailabilityZone != nil {
		az = *input.AvailabilityZone
	} else if s.config != nil {
		az = s.config.AZ
	}

	subnetID := utils.GenerateResourceID("subnet")

	// Calculate available IPs (total hosts minus AWS reserved: network, router, DNS, future, broadcast)
	// ones is validated to be 16-28 above, so (32-ones) is always 4-16 and safe for uint conversion
	totalHosts := max((1<<(32-ones))-5, 0) //#nosec G115 - ones validated 16-28

	record := SubnetRecord{
		SubnetId:         subnetID,
		VpcId:            vpcID,
		CidrBlock:        subnetNet.String(),
		AvailabilityZone: az,
		State:            "available",
		IsDefault:        false,
		Tags:             make(map[string]string),
		CreatedAt:        time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "subnet" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subnet record: %w", err)
	}
	if _, err := s.subnetKV.Put(utils.AccountKey(accountID, subnetID), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateSubnet completed", "subnetId", subnetID, "vpcId", vpcID, "cidrBlock", record.CidrBlock, "accountID", accountID)

	// Publish vpc.create-subnet event for vpcd topology translation
	s.publishSubnetEvent("vpc.create-subnet", record.SubnetId, record.VpcId, record.CidrBlock)

	return &ec2.CreateSubnetOutput{
		Subnet: s.subnetRecordToEC2(&record, totalHosts, accountID),
	}, nil
}

// DeleteSubnet deletes a subnet
func (s *VPCServiceImpl) DeleteSubnet(input *ec2.DeleteSubnetInput, accountID string) (*ec2.DeleteSubnetOutput, error) {
	if input.SubnetId == nil || *input.SubnetId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	subnetID := *input.SubnetId
	key := utils.AccountKey(accountID, subnetID)

	// Read subnet record before deletion (needed for vpcd event)
	subnetEntry, err := s.subnetKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidSubnetIDNotFound)
	}
	var subnetRecord SubnetRecord
	_ = json.Unmarshal(subnetEntry.Value(), &subnetRecord)

	if err := s.subnetKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteSubnet completed", "subnetId", subnetID, "accountID", accountID)

	// Publish vpc.delete-subnet event for vpcd topology cleanup
	s.publishSubnetEvent("vpc.delete-subnet", subnetID, subnetRecord.VpcId, subnetRecord.CidrBlock)

	return &ec2.DeleteSubnetOutput{}, nil
}

// DescribeSubnets describes subnets
func (s *VPCServiceImpl) DescribeSubnets(input *ec2.DescribeSubnetsInput, accountID string) (*ec2.DescribeSubnetsOutput, error) {
	var subnets []*ec2.Subnet

	subnetIDs := make(map[string]bool)
	for _, id := range input.SubnetIds {
		if id != nil {
			subnetIDs[*id] = true
		}
	}

	// Extract VPC ID filter if present
	vpcIDFilter := ""
	for _, f := range input.Filters {
		if f.Name != nil && *f.Name == "vpc-id" && len(f.Values) > 0 && f.Values[0] != nil {
			vpcIDFilter = *f.Values[0]
		}
	}

	prefix := accountID + "."
	keys, err := s.subnetKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.subnetKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get subnet record", "key", key, "error", err)
			continue
		}

		var record SubnetRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal subnet record", "key", key, "error", err)
			continue
		}

		if len(subnetIDs) > 0 && !subnetIDs[record.SubnetId] {
			continue
		}

		// Apply VPC ID filter
		if vpcIDFilter != "" && record.VpcId != vpcIDFilter {
			continue
		}

		// Calculate available IPs
		_, subnetNet, err := net.ParseCIDR(record.CidrBlock)
		availableIPs := 0
		if err == nil {
			ones, _ := subnetNet.Mask.Size()
			availableIPs = max((1<<(32-ones))-5, 0) //#nosec G115 - ones from validated CIDR
		}

		subnets = append(subnets, s.subnetRecordToEC2(&record, availableIPs, accountID))
	}

	// If specific subnet IDs were requested but not found, return error
	if len(subnetIDs) > 0 {
		found := make(map[string]bool)
		for _, subnet := range subnets {
			if subnet.SubnetId != nil {
				found[*subnet.SubnetId] = true
			}
		}
		for id := range subnetIDs {
			if !found[id] {
				return nil, errors.New(awserrors.ErrorInvalidSubnetIDNotFound)
			}
		}
	}

	slog.Info("DescribeSubnets completed", "count", len(subnets), "accountID", accountID)

	return &ec2.DescribeSubnetsOutput{
		Subnets: subnets,
	}, nil
}

func (s *VPCServiceImpl) vpcRecordToEC2(record *VPCRecord, accountID string) *ec2.Vpc {
	vpc := &ec2.Vpc{
		VpcId:     aws.String(record.VpcId),
		CidrBlock: aws.String(record.CidrBlock),
		State:     aws.String(record.State),
		IsDefault: aws.Bool(record.IsDefault),
		OwnerId:   aws.String(accountID),
		CidrBlockAssociationSet: []*ec2.VpcCidrBlockAssociation{
			{
				CidrBlock: aws.String(record.CidrBlock),
				CidrBlockState: &ec2.VpcCidrBlockState{
					State: aws.String("associated"),
				},
				AssociationId: aws.String(fmt.Sprintf("vpc-cidr-assoc-%s", record.VpcId[4:])),
			},
		},
		DhcpOptionsId:   aws.String("dopt-default"),
		InstanceTenancy: aws.String("default"),
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		vpc.Tags = tags
	}

	return vpc
}

func (s *VPCServiceImpl) subnetRecordToEC2(record *SubnetRecord, availableIPs int, accountID string) *ec2.Subnet {
	subnet := &ec2.Subnet{
		SubnetId:                aws.String(record.SubnetId),
		VpcId:                   aws.String(record.VpcId),
		CidrBlock:               aws.String(record.CidrBlock),
		AvailabilityZone:        aws.String(record.AvailabilityZone),
		State:                   aws.String(record.State),
		DefaultForAz:            aws.Bool(record.IsDefault),
		AvailableIpAddressCount: aws.Int64(int64(availableIPs)),
		OwnerId:                 aws.String(accountID),
		MapPublicIpOnLaunch:     aws.Bool(false),
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		subnet.Tags = tags
	}

	return subnet
}

// Default VPC constants matching AWS defaults.
const (
	DefaultVPCCidr    = "172.31.0.0/16"
	DefaultSubnetCidr = "172.31.0.0/20"
)

// EnsureDefaultVPC creates a default VPC and subnet if none exists for the given account.
// This matches AWS behavior where a default VPC is present on account creation.
// Safe to call multiple times — no-ops if a default VPC already exists.
func (s *VPCServiceImpl) EnsureDefaultVPC(accountID string) error {
	if s.vpcKV == nil {
		return nil // No persistence, skip
	}

	// Check if a default VPC already exists for this account
	prefix := accountID + "."
	keys, err := s.vpcKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return fmt.Errorf("list VPCs: %w", err)
	}

	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		entry, err := s.vpcKV.Get(key)
		if err != nil {
			continue
		}
		var record VPCRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}
		if record.IsDefault {
			slog.Debug("Default VPC already exists", "vpcId", record.VpcId, "accountID", accountID)
			return nil
		}
	}

	// Create default VPC
	vni, err := s.nextVNI()
	if err != nil {
		return fmt.Errorf("allocate VNI for default VPC: %w", err)
	}

	vpcID := utils.GenerateResourceID("vpc")
	vpcRecord := VPCRecord{
		VpcId:     vpcID,
		CidrBlock: DefaultVPCCidr,
		State:     "available",
		IsDefault: true,
		VNI:       vni,
		Tags:      map[string]string{"Name": "default"},
		CreatedAt: time.Now(),
	}

	data, err := json.Marshal(vpcRecord)
	if err != nil {
		return fmt.Errorf("marshal default VPC: %w", err)
	}
	if _, err := s.vpcKV.Put(utils.AccountKey(accountID, vpcID), data); err != nil {
		return fmt.Errorf("store default VPC: %w", err)
	}

	s.publishVPCEvent("vpc.create", vpcID, DefaultVPCCidr, vni)

	// Determine AZ
	az := "us-east-1a"
	if s.config != nil && s.config.AZ != "" {
		az = s.config.AZ
	}

	// Create default subnet
	subnetID := utils.GenerateResourceID("subnet")
	subnetRecord := SubnetRecord{
		SubnetId:         subnetID,
		VpcId:            vpcID,
		CidrBlock:        DefaultSubnetCidr,
		AvailabilityZone: az,
		State:            "available",
		IsDefault:        true,
		Tags:             map[string]string{"Name": "default"},
		CreatedAt:        time.Now(),
	}

	data, err = json.Marshal(subnetRecord)
	if err != nil {
		return fmt.Errorf("marshal default subnet: %w", err)
	}
	if _, err := s.subnetKV.Put(utils.AccountKey(accountID, subnetID), data); err != nil {
		return fmt.Errorf("store default subnet: %w", err)
	}

	s.publishSubnetEvent("vpc.create-subnet", subnetID, vpcID, DefaultSubnetCidr)

	slog.Info("Created default VPC and subnet",
		"vpcId", vpcID,
		"vpcCidr", DefaultVPCCidr,
		"subnetId", subnetID,
		"subnetCidr", DefaultSubnetCidr,
		"az", az,
		"accountID", accountID,
	)
	return nil
}

// GetDefaultSubnet returns the default subnet for RunInstances when no SubnetId is specified.
func (s *VPCServiceImpl) GetDefaultSubnet(accountID string) (*SubnetRecord, error) {
	prefix := accountID + "."
	keys, err := s.subnetKV.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return nil, fmt.Errorf("no default subnet found")
		}
		return nil, fmt.Errorf("list subnets: %w", err)
	}

	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		entry, err := s.subnetKV.Get(key)
		if err != nil {
			continue
		}
		var record SubnetRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}
		if record.IsDefault {
			return &record, nil
		}
	}

	return nil, fmt.Errorf("no default subnet found")
}

// publishVPCEvent publishes a VPC lifecycle event to NATS for vpcd consumption.
// This is fire-and-forget; errors are logged but do not fail the API response.
func (s *VPCServiceImpl) publishVPCEvent(topic, vpcId, cidrBlock string, vni int64) {
	if s.natsConn == nil {
		return
	}
	evt := struct {
		VpcId     string `json:"vpc_id"`
		CidrBlock string `json:"cidr_block"`
		VNI       int64  `json:"vni"`
	}{VpcId: vpcId, CidrBlock: cidrBlock, VNI: vni}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("Failed to marshal VPC event", "topic", topic, "err", err)
		return
	}
	if err := s.natsConn.Publish(topic, data); err != nil {
		slog.Error("Failed to publish VPC event", "topic", topic, "err", err)
	}
}

// publishSubnetEvent publishes a subnet lifecycle event to NATS for vpcd consumption.
func (s *VPCServiceImpl) publishSubnetEvent(topic, subnetId, vpcId, cidrBlock string) {
	if s.natsConn == nil {
		return
	}
	evt := struct {
		SubnetId  string `json:"subnet_id"`
		VpcId     string `json:"vpc_id"`
		CidrBlock string `json:"cidr_block"`
	}{SubnetId: subnetId, VpcId: vpcId, CidrBlock: cidrBlock}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("Failed to marshal subnet event", "topic", topic, "err", err)
		return
	}
	if err := s.natsConn.Publish(topic, data); err != nil {
		slog.Error("Failed to publish subnet event", "topic", topic, "err", err)
	}
}
