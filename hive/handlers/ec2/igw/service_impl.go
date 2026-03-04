package handlers_ec2_igw

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// Ensure IGWServiceImpl implements IGWService
var _ IGWService = (*IGWServiceImpl)(nil)

const KVBucketIGW = "hive-igw"
const kvBucketVPCs = "hive-vpc-vpcs"

// IGWRecord represents a stored Internet Gateway
type IGWRecord struct {
	InternetGatewayId string            `json:"internet_gateway_id"`
	VpcId             string            `json:"vpc_id,omitempty"` // empty when detached
	State             string            `json:"state"`            // "available", "attached", "detached"
	Tags              map[string]string `json:"tags"`
	CreatedAt         time.Time         `json:"created_at"`
}

// IGWServiceImpl implements Internet Gateway operations with NATS JetStream persistence
type IGWServiceImpl struct {
	config   *config.Config
	igwKV    nats.KeyValue
	vpcKV    nats.KeyValue
	natsConn *nats.Conn
}

// NewIGWServiceImpl creates a new Internet Gateway service without NATS persistence
func NewIGWServiceImpl(cfg *config.Config) *IGWServiceImpl {
	return &IGWServiceImpl{
		config: cfg,
	}
}

// NewIGWServiceImplWithNATS creates an Internet Gateway service with NATS JetStream for persistence
func NewIGWServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*IGWServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	igwKV, err := getOrCreateKVBucket(js, KVBucketIGW, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketIGW, err)
	}

	// Get VPC KV bucket for cross-resource ownership validation
	vpcKV, err := js.KeyValue(kvBucketVPCs)
	if err != nil {
		slog.Warn("IGW service: VPC KV bucket not available, VPC ownership checks disabled", "error", err)
	}

	slog.Info("IGW service initialized with JetStream KV", "bucket", KVBucketIGW)

	return &IGWServiceImpl{
		config:   cfg,
		igwKV:    igwKV,
		vpcKV:    vpcKV,
		natsConn: natsConn,
	}, nil
}

func getOrCreateKVBucket(js nats.JetStreamContext, bucketName string, history int) (nats.KeyValue, error) {
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		History: utils.SafeIntToUint8(history),
	})
	if err != nil {
		kv, err = js.KeyValue(bucketName)
		if err != nil {
			return nil, err
		}
	}
	return kv, nil
}

func accountKey(accountID, resourceID string) string {
	return accountID + "." + resourceID
}

// CreateInternetGateway creates a new Internet Gateway (initially detached)
func (s *IGWServiceImpl) CreateInternetGateway(input *ec2.CreateInternetGatewayInput, accountID string) (*ec2.CreateInternetGatewayOutput, error) {
	igwID := utils.GenerateResourceID("igw")

	record := IGWRecord{
		InternetGatewayId: igwID,
		State:             "available",
		Tags:              make(map[string]string),
		CreatedAt:         time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "internet-gateway" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal IGW record: %w", err)
	}
	if _, err := s.igwKV.Put(accountKey(accountID, igwID), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateInternetGateway completed", "internetGatewayId", igwID, "accountID", accountID)

	return &ec2.CreateInternetGatewayOutput{
		InternetGateway: s.recordToEC2(&record),
	}, nil
}

// DeleteInternetGateway deletes an Internet Gateway (must be detached first)
func (s *IGWServiceImpl) DeleteInternetGateway(input *ec2.DeleteInternetGatewayInput, accountID string) (*ec2.DeleteInternetGatewayOutput, error) {
	if input.InternetGatewayId == nil || *input.InternetGatewayId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	igwID := *input.InternetGatewayId
	key := accountKey(accountID, igwID)

	entry, err := s.igwKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
	}

	var record IGWRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Cannot delete an attached IGW
	if record.VpcId != "" {
		return nil, errors.New(awserrors.ErrorDependencyViolation)
	}

	if err := s.igwKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteInternetGateway completed", "internetGatewayId", igwID, "accountID", accountID)

	return &ec2.DeleteInternetGatewayOutput{}, nil
}

// DescribeInternetGateways lists Internet Gateways, optionally filtered by ID
func (s *IGWServiceImpl) DescribeInternetGateways(input *ec2.DescribeInternetGatewaysInput, accountID string) (*ec2.DescribeInternetGatewaysOutput, error) {
	var igws []*ec2.InternetGateway

	igwIDs := make(map[string]bool)
	for _, id := range input.InternetGatewayIds {
		if id != nil {
			igwIDs[*id] = true
		}
	}

	prefix := accountID + "."
	keys, err := s.igwKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	foundIDs := make(map[string]bool)

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.igwKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get IGW record", "key", key, "error", err)
			continue
		}

		var record IGWRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal IGW record", "key", key, "error", err)
			continue
		}

		if len(igwIDs) > 0 && !igwIDs[record.InternetGatewayId] {
			continue
		}

		igws = append(igws, s.recordToEC2(&record))
		foundIDs[record.InternetGatewayId] = true
	}

	// Return error if specific IDs were requested but not found
	for id := range igwIDs {
		if !foundIDs[id] {
			return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
		}
	}

	slog.Info("DescribeInternetGateways completed", "count", len(igws), "accountID", accountID)

	return &ec2.DescribeInternetGatewaysOutput{
		InternetGateways: igws,
	}, nil
}

// AttachInternetGateway attaches an IGW to a VPC and publishes a NATS event
// for vpcd to create the OVN external switch, gateway port, and SNAT rules.
func (s *IGWServiceImpl) AttachInternetGateway(input *ec2.AttachInternetGatewayInput, accountID string) (*ec2.AttachInternetGatewayOutput, error) {
	if input.InternetGatewayId == nil || *input.InternetGatewayId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	igwID := *input.InternetGatewayId
	vpcID := *input.VpcId
	key := accountKey(accountID, igwID)

	entry, err := s.igwKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
	}

	var record IGWRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	if record.VpcId != "" {
		return nil, errors.New(awserrors.ErrorResourceAlreadyAssociated)
	}

	// Verify the caller owns the target VPC
	if s.vpcKV != nil {
		if _, err := s.vpcKV.Get(accountKey(accountID, vpcID)); err != nil {
			slog.Warn("AttachInternetGateway: VPC not found for account", "vpcId", vpcID, "accountID", accountID)
			return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
		}
	}

	record.VpcId = vpcID
	record.State = "attached"

	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.igwKV.Put(key, data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Publish event for vpcd to create OVN external switch + gateway + SNAT
	if s.natsConn != nil {
		event := IGWAttachEvent{
			InternetGatewayId: igwID,
			VpcId:             vpcID,
		}
		eventData, _ := json.Marshal(event)
		if err := s.natsConn.Publish("vpc.igw-attach", eventData); err != nil {
			slog.Warn("Failed to publish IGW attach event", "error", err)
		}
	}

	slog.Info("AttachInternetGateway completed", "internetGatewayId", igwID, "vpcId", vpcID, "accountID", accountID)

	return &ec2.AttachInternetGatewayOutput{}, nil
}

// DetachInternetGateway detaches an IGW from a VPC and publishes a NATS event
// for vpcd to clean up the OVN external switch, gateway port, and NAT rules.
func (s *IGWServiceImpl) DetachInternetGateway(input *ec2.DetachInternetGatewayInput, accountID string) (*ec2.DetachInternetGatewayOutput, error) {
	if input.InternetGatewayId == nil || *input.InternetGatewayId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	igwID := *input.InternetGatewayId
	vpcID := *input.VpcId
	key := accountKey(accountID, igwID)

	entry, err := s.igwKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
	}

	var record IGWRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	if record.VpcId != vpcID {
		return nil, errors.New(awserrors.ErrorGatewayNotAttached)
	}

	record.VpcId = ""
	record.State = "available"

	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.igwKV.Put(key, data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Publish event for vpcd to clean up OVN external switch + gateway + NAT
	if s.natsConn != nil {
		event := IGWDetachEvent{
			InternetGatewayId: igwID,
			VpcId:             vpcID,
		}
		eventData, _ := json.Marshal(event)
		if err := s.natsConn.Publish("vpc.igw-detach", eventData); err != nil {
			slog.Warn("Failed to publish IGW detach event", "error", err)
		}
	}

	slog.Info("DetachInternetGateway completed", "internetGatewayId", igwID, "vpcId", vpcID, "accountID", accountID)

	return &ec2.DetachInternetGatewayOutput{}, nil
}

func (s *IGWServiceImpl) recordToEC2(record *IGWRecord) *ec2.InternetGateway {
	igw := &ec2.InternetGateway{
		InternetGatewayId: aws.String(record.InternetGatewayId),
	}

	if record.VpcId != "" {
		igw.Attachments = []*ec2.InternetGatewayAttachment{
			{
				VpcId: aws.String(record.VpcId),
				State: aws.String(record.State),
			},
		}
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		igw.Tags = tags
	}

	return igw
}

// IGWAttachEvent is published to NATS when an IGW is attached to a VPC.
type IGWAttachEvent struct {
	InternetGatewayId string `json:"internet_gateway_id"`
	VpcId             string `json:"vpc_id"`
}

// IGWDetachEvent is published to NATS when an IGW is detached from a VPC.
type IGWDetachEvent struct {
	InternetGatewayId string `json:"internet_gateway_id"`
	VpcId             string `json:"vpc_id"`
}
