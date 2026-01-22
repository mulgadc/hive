package handlers_ec2_volume

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSVolumeService handles volume operations via NATS messaging
type NATSVolumeService struct {
	natsConn *nats.Conn
}

// NewNATSVolumeService creates a new NATS-based volume service
func NewNATSVolumeService(conn *nats.Conn) VolumeService {
	return &NATSVolumeService{natsConn: conn}
}

// DescribeVolumes sends a request via NATS and waits for response
func (s *NATSVolumeService) DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send NATS request with 30 second timeout
	msg, err := s.natsConn.Request("ec2.DescribeVolumes", jsonData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Validate error response
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.DescribeVolumesOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}
