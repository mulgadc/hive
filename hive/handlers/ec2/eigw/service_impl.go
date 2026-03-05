package handlers_ec2_eigw

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
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// Ensure EgressOnlyIGWServiceImpl implements EgressOnlyIGWService
var _ EgressOnlyIGWService = (*EgressOnlyIGWServiceImpl)(nil)

const KVBucketEgressOnlyIGW = "hive-eigw"

// EgressOnlyIGWRecord represents a stored Egress-only Internet Gateway
type EgressOnlyIGWRecord struct {
	EgressOnlyInternetGatewayId string            `json:"egress_only_internet_gateway_id"`
	VpcId                       string            `json:"vpc_id"`
	State                       string            `json:"state"`
	Tags                        map[string]string `json:"tags"`
	CreatedAt                   time.Time         `json:"created_at"`
}

// EgressOnlyIGWServiceImpl implements Egress-only Internet Gateway operations with NATS JetStream persistence
type EgressOnlyIGWServiceImpl struct {
	config *config.Config
	eigwKV nats.KeyValue
	vpcKV  nats.KeyValue
}

// NewEgressOnlyIGWServiceImplWithNATS creates an Egress-only Internet Gateway service with NATS JetStream for persistence
func NewEgressOnlyIGWServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*EgressOnlyIGWServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	eigwKV, err := utils.GetOrCreateKVBucket(js, KVBucketEgressOnlyIGW, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketEgressOnlyIGW, err)
	}

	// Get VPC KV bucket for cross-resource ownership validation
	vpcKV, err := js.KeyValue(handlers_ec2_vpc.KVBucketVPCs)
	if err != nil {
		slog.Warn("EIGW service: VPC KV bucket not available, VPC ownership checks disabled", "error", err)
	}

	slog.Info("Egress-only IGW service initialized with JetStream KV", "bucket", KVBucketEgressOnlyIGW)

	return &EgressOnlyIGWServiceImpl{
		config: cfg,
		eigwKV: eigwKV,
		vpcKV:  vpcKV,
	}, nil
}

// CreateEgressOnlyInternetGateway creates a new Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) CreateEgressOnlyInternetGateway(input *ec2.CreateEgressOnlyInternetGatewayInput, accountID string) (*ec2.CreateEgressOnlyInternetGatewayOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	// Verify the caller owns the target VPC (fail-closed if KV unavailable)
	if s.vpcKV == nil {
		slog.Error("VPC KV unavailable, cannot verify VPC ownership")
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.vpcKV.Get(utils.AccountKey(accountID, *input.VpcId)); err != nil {
		slog.Warn("CreateEgressOnlyInternetGateway: VPC not found for account", "vpcId", *input.VpcId, "accountID", accountID)
		return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
	}

	eigwID := utils.GenerateResourceID("eigw")

	record := EgressOnlyIGWRecord{
		EgressOnlyInternetGatewayId: eigwID,
		VpcId:                       *input.VpcId,
		State:                       "attached",
		Tags:                        make(map[string]string),
		CreatedAt:                   time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "egress-only-internet-gateway" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Egress-only IGW record: %w", err)
	}
	if _, err := s.eigwKV.Put(utils.AccountKey(accountID, eigwID), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID, "vpcId", record.VpcId, "accountID", accountID)

	return &ec2.CreateEgressOnlyInternetGatewayOutput{
		EgressOnlyInternetGateway: s.recordToEC2(&record),
	}, nil
}

// DeleteEgressOnlyInternetGateway deletes an Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) DeleteEgressOnlyInternetGateway(input *ec2.DeleteEgressOnlyInternetGatewayInput, accountID string) (*ec2.DeleteEgressOnlyInternetGatewayOutput, error) {
	if input.EgressOnlyInternetGatewayId == nil || *input.EgressOnlyInternetGatewayId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	eigwID := *input.EgressOnlyInternetGatewayId
	key := utils.AccountKey(accountID, eigwID)

	// Verify the EIGW exists before deleting
	if _, err := s.eigwKV.Get(key); err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorInvalidEgressOnlyInternetGatewayIdNotFound)
		}
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if err := s.eigwKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID, "accountID", accountID)

	return &ec2.DeleteEgressOnlyInternetGatewayOutput{
		ReturnCode: aws.Bool(true),
	}, nil
}

// DescribeEgressOnlyInternetGateways describes Egress-only Internet Gateways
func (s *EgressOnlyIGWServiceImpl) DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput, accountID string) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error) {
	var egressOnlyIGWs []*ec2.EgressOnlyInternetGateway

	eigwIDs := make(map[string]bool)
	for _, id := range input.EgressOnlyInternetGatewayIds {
		if id != nil {
			eigwIDs[*id] = true
		}
	}

	prefix := accountID + "."
	keys, err := s.eigwKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.eigwKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get Egress-only IGW record", "key", key, "error", err)
			continue
		}

		var record EgressOnlyIGWRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal Egress-only IGW record", "key", key, "error", err)
			continue
		}

		if len(eigwIDs) > 0 && !eigwIDs[record.EgressOnlyInternetGatewayId] {
			continue
		}

		egressOnlyIGWs = append(egressOnlyIGWs, s.recordToEC2(&record))
	}

	slog.Info("DescribeEgressOnlyInternetGateways completed", "count", len(egressOnlyIGWs), "accountID", accountID)

	return &ec2.DescribeEgressOnlyInternetGatewaysOutput{
		EgressOnlyInternetGateways: egressOnlyIGWs,
	}, nil
}

func (s *EgressOnlyIGWServiceImpl) recordToEC2(record *EgressOnlyIGWRecord) *ec2.EgressOnlyInternetGateway {
	eigw := &ec2.EgressOnlyInternetGateway{
		EgressOnlyInternetGatewayId: aws.String(record.EgressOnlyInternetGatewayId),
		Attachments: []*ec2.InternetGatewayAttachment{
			{
				VpcId: aws.String(record.VpcId),
				State: aws.String(record.State),
			},
		},
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		eigw.Tags = tags
	}

	return eigw
}
