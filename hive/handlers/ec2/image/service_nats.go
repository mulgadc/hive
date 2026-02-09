package handlers_ec2_image

import (
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

func (s *NATSImageService) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	return utils.NATSRequest[ec2.DescribeImagesOutput](s.natsConn, "ec2.DescribeImages", input, 30*time.Second)
}

// Stub implementations for unimplemented ImageService methods
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
