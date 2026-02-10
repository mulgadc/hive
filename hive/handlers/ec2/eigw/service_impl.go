package handlers_ec2_eigw

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	"github.com/nats-io/nats.go"
)

// Ensure EgressOnlyIGWServiceImpl implements EgressOnlyIGWService
var _ EgressOnlyIGWService = (*EgressOnlyIGWServiceImpl)(nil)

const (
	KVBucketEgressOnlyIGW = "hive-eigw"
	HiveAccountID         = "000000000000"
)

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
	js     nats.JetStreamContext
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
		slog.Warn("Failed to create Egress-only IGW KV bucket", "error", err)
		return NewEgressOnlyIGWServiceImpl(cfg), nil
	}

	slog.Info("Egress-only IGW service initialized with JetStream KV", "bucket", KVBucketEgressOnlyIGW)

	return &EgressOnlyIGWServiceImpl{
		config: cfg,
		js:     js,
		eigwKV: eigwKV,
	}, nil
}

func getOrCreateKVBucket(js nats.JetStreamContext, bucketName string, history int) (nats.KeyValue, error) {
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		History: uint8(history),
	})
	if err != nil {
		kv, err = js.KeyValue(bucketName)
		if err != nil {
			return nil, err
		}
	}
	return kv, nil
}

// generateEgressOnlyIGWID generates a unique Egress-only Internet Gateway ID
func generateEgressOnlyIGWID() string {
	return fmt.Sprintf("eigw-%08x%08x", rand.Uint32(), rand.Uint32())
}

// CreateEgressOnlyInternetGateway creates a new Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) CreateEgressOnlyInternetGateway(input *ec2.CreateEgressOnlyInternetGatewayInput) (*ec2.CreateEgressOnlyInternetGatewayOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, fmt.Errorf("InvalidParameterValue: VpcId is required")
	}

	eigwID := generateEgressOnlyIGWID()

	record := EgressOnlyIGWRecord{
		EgressOnlyInternetGatewayId: eigwID,
		VpcId:                       *input.VpcId,
		State:                       "attached",
		Tags:                        make(map[string]string),
		CreatedAt:                   time.Now(),
	}

	// Process tags
	if input.TagSpecifications != nil {
		for _, tagSpec := range input.TagSpecifications {
			if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "egress-only-internet-gateway" {
				for _, tag := range tagSpec.Tags {
					if tag.Key != nil && tag.Value != nil {
						record.Tags[*tag.Key] = *tag.Value
					}
				}
			}
		}
	}

	// Store in KV
	if s.eigwKV != nil {
		data, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Egress-only IGW record: %w", err)
		}
		if _, err := s.eigwKV.Put(eigwID, data); err != nil {
			slog.Error("Failed to store Egress-only IGW record", "error", err)
			return nil, fmt.Errorf("ServerInternal: failed to store Egress-only IGW")
		}
	}

	slog.Info("CreateEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID, "vpcId", record.VpcId)

	return &ec2.CreateEgressOnlyInternetGatewayOutput{
		EgressOnlyInternetGateway: s.recordToEC2(&record),
	}, nil
}

// DeleteEgressOnlyInternetGateway deletes an Egress-only Internet Gateway
func (s *EgressOnlyIGWServiceImpl) DeleteEgressOnlyInternetGateway(input *ec2.DeleteEgressOnlyInternetGatewayInput) (*ec2.DeleteEgressOnlyInternetGatewayOutput, error) {
	if input.EgressOnlyInternetGatewayId == nil || *input.EgressOnlyInternetGatewayId == "" {
		return nil, fmt.Errorf("InvalidParameterValue: EgressOnlyInternetGatewayId is required")
	}

	eigwID := *input.EgressOnlyInternetGatewayId

	// Check if exists
	if s.eigwKV != nil {
		_, err := s.eigwKV.Get(eigwID)
		if err != nil {
			return nil, fmt.Errorf("InvalidEgressOnlyInternetGatewayId.NotFound: %s", eigwID)
		}

		// Delete from KV
		if err := s.eigwKV.Delete(eigwID); err != nil {
			slog.Error("Failed to delete Egress-only IGW record", "error", err)
		}
	}

	slog.Info("DeleteEgressOnlyInternetGateway completed", "egressOnlyInternetGatewayId", eigwID)

	return &ec2.DeleteEgressOnlyInternetGatewayOutput{
		ReturnCode: aws.Bool(true),
	}, nil
}

// DescribeEgressOnlyInternetGateways describes Egress-only Internet Gateways
func (s *EgressOnlyIGWServiceImpl) DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error) {
	var egressOnlyIGWs []*ec2.EgressOnlyInternetGateway

	// Build filter sets
	eigwIDs := make(map[string]bool)
	if input.EgressOnlyInternetGatewayIds != nil {
		for _, id := range input.EgressOnlyInternetGatewayIds {
			if id != nil {
				eigwIDs[*id] = true
			}
		}
	}

	if s.eigwKV != nil {
		keys, err := s.eigwKV.Keys()
		if err != nil {
			slog.Warn("Failed to list Egress-only IGW keys", "error", err)
		} else {
			for _, key := range keys {
				// Apply ID filter
				if len(eigwIDs) > 0 && !eigwIDs[key] {
					continue
				}

				entry, err := s.eigwKV.Get(key)
				if err != nil {
					continue
				}

				var record EgressOnlyIGWRecord
				if err := json.Unmarshal(entry.Value(), &record); err != nil {
					continue
				}

				egressOnlyIGWs = append(egressOnlyIGWs, s.recordToEC2(&record))
			}
		}
	}

	slog.Info("DescribeEgressOnlyInternetGateways completed", "count", len(egressOnlyIGWs))

	return &ec2.DescribeEgressOnlyInternetGatewaysOutput{
		EgressOnlyInternetGateways: egressOnlyIGWs,
	}, nil
}

// Helper methods

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

	// Add tags
	if len(record.Tags) > 0 {
		for k, v := range record.Tags {
			eigw.Tags = append(eigw.Tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
	}

	return eigw
}
