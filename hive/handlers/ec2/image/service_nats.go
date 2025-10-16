package handlers_ec2_image

import (
	"github.com/aws/aws-sdk-go/service/ec2"
)

// NATSImageService handles image operations via NATS messaging
// This will be implemented in Phase 2
type NATSImageService struct {
	// natsConn *nats.Conn - will be added in Phase 2
}

// NewNATSImageService creates a new NATS-based image service
// func NewNATSImageService(conn *nats.Conn) ImageService {
// 	return &NATSImageService{natsConn: conn}
// }

// TODO Phase 2: Implement NATS-based operations
// These will publish requests to NATS topics and wait for responses

func (s *NATSImageService) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	// TODO: Publish to NATS topic, wait for response
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}

func (s *NATSImageService) ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error) {
	panic("NATS service not yet implemented - use MockImageService for testing")
}
