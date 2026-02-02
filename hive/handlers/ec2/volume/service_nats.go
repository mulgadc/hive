package handlers_ec2_volume

import (
	"encoding/json"
	"errors"
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

// CreateVolume sends a CreateVolume request via NATS and waits for response
func (s *NATSVolumeService) CreateVolume(input *ec2.CreateVolumeInput) (*ec2.Volume, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	msg, err := s.natsConn.Request("ec2.CreateVolume", jsonData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, errors.New(*responseError.Code)
	}

	var output ec2.Volume
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

// DescribeVolumes sends a DescribeVolumes request via NATS and waits for response
func (s *NATSVolumeService) DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	msg, err := s.natsConn.Request("ec2.DescribeVolumes", jsonData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, errors.New(*responseError.Code)
	}

	var output ec2.DescribeVolumesOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

// ModifyVolume sends a ModifyVolume request via NATS and waits for response
func (s *NATSVolumeService) ModifyVolume(input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error) {
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	msg, err := s.natsConn.Request("ec2.ModifyVolume", jsonData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, errors.New(*responseError.Code)
	}

	var output ec2.ModifyVolumeOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}
