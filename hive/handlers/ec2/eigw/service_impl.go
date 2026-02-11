package handlers_ec2_eigw

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
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
}

// NewEgressOnlyIGWServiceImpl creates a new Egress-only Internet Gateway service implementation
func NewEgressOnlyIGWServiceImpl(cfg *config.Config) *EgressOnlyIGWServiceImpl {
	return &EgressOnlyIGWServiceImpl{
		config: cfg,
	}
}

// NewEgressOnlyIGWServiceImplWithNATS creates an Egress-only Internet Gateway service with NATS JetStream for persistence
func NewEgressOnlyIGWServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*EgressOnlyIGWServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	eigwKV, err := getOrCreateKVBucket(js, KVBucketEgressOnlyIGW, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketEgressOnlyIGW, err)
	}

	slog.Info("Egress-only IGW service initialized with JetStream KV", "bucket", KVBucketEgressOnlyIGW)

	return &EgressOnlyIGWServiceImpl{
		config: cfg,
		eigwKV: eigwKV,
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

// CreateEgressOnlyInternetGateway creates a new Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) CreateEgressOnlyInternetGateway(input *ec2.CreateEgressOnlyInternetGatewayInput) (*ec2.CreateEgressOnlyInternetGatewayOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
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
	if _, err := s.eigwKV.Put(eigwID, data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID, "vpcId", record.VpcId)

	return &ec2.CreateEgressOnlyInternetGatewayOutput{
		EgressOnlyInternetGateway: s.recordToEC2(&record),
	}, nil
}

// DeleteEgressOnlyInternetGateway deletes an Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) DeleteEgressOnlyInternetGateway(input *ec2.DeleteEgressOnlyInternetGatewayInput) (*ec2.DeleteEgressOnlyInternetGatewayOutput, error) {
	if input.EgressOnlyInternetGatewayId == nil || *input.EgressOnlyInternetGatewayId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	eigwID := *input.EgressOnlyInternetGatewayId

	if _, err := s.eigwKV.Get(eigwID); err != nil {
		return nil, errors.New(awserrors.ErrorInvalidEgressOnlyInternetGatewayIdNotFound)
	}

	if err := s.eigwKV.Delete(eigwID); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID)

	return &ec2.DeleteEgressOnlyInternetGatewayOutput{
		ReturnCode: aws.Bool(true),
	}, nil
}

// DescribeEgressOnlyInternetGateways describes Egress-only Internet Gateways
func (s *EgressOnlyIGWServiceImpl) DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error) {
	var egressOnlyIGWs []*ec2.EgressOnlyInternetGateway

	eigwIDs := make(map[string]bool)
	for _, id := range input.EgressOnlyInternetGatewayIds {
		if id != nil {
			eigwIDs[*id] = true
		}
	}

	keys, err := s.eigwKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if len(eigwIDs) > 0 && !eigwIDs[key] {
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

		egressOnlyIGWs = append(egressOnlyIGWs, s.recordToEC2(&record))
	}

	slog.Info("DescribeEgressOnlyInternetGateways completed", "count", len(egressOnlyIGWs))

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
