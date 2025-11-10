package handlers_ec2_image

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSImageService handles image operations via NATS messaging
type NATSImageService struct {
	natsConn *nats.Conn
}

// NewNATSImageService creates a new NATS-based image service
func NewNATSImageService(conn *nats.Conn) ImageService {
	return &NATSImageService{natsConn: conn}
}

// DescribeImages sends a request via NATS and waits for response
func (s *NATSImageService) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	// Marshal input to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Send NATS request with 30 second timeout
	msg, err := s.natsConn.Request("ec2.DescribeImages", jsonData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	// Validate error response
	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("daemon returned error: %s", *responseError.Code)
	}

	// Unmarshal successful response
	var output ec2.DescribeImagesOutput
	err = json.Unmarshal(msg.Data, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &output, nil
}

// Stub implementations for other ImageService methods
func (s *NATSImageService) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	return nil, fmt.Errorf("CreateImage not yet implemented")
}

func (s *NATSImageService) CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error) {
	return nil, fmt.Errorf("CopyImage not yet implemented")
}

func (s *NATSImageService) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error) {
	return nil, fmt.Errorf("DescribeImageAttribute not yet implemented")
}

func (s *NATSImageService) RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error) {
	return nil, fmt.Errorf("RegisterImage not yet implemented")
}

func (s *NATSImageService) DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error) {
	return nil, fmt.Errorf("DeregisterImage not yet implemented")
}

func (s *NATSImageService) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error) {
	return nil, fmt.Errorf("ModifyImageAttribute not yet implemented")
}

func (s *NATSImageService) ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error) {
	return nil, fmt.Errorf("ResetImageAttribute not yet implemented")
}
