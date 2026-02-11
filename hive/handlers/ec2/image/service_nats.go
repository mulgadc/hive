package handlers_ec2_image

import (
	"errors"
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

func (s *NATSImageService) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	return utils.NATSRequest[ec2.CreateImageOutput](s.natsConn, "ec2.CreateImage", input, 120*time.Second)
}

func (s *NATSImageService) CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error) {
	return nil, errors.New("CopyImage not yet implemented")
}

func (s *NATSImageService) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error) {
	return nil, errors.New("DescribeImageAttribute not yet implemented")
}

func (s *NATSImageService) RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error) {
	return nil, errors.New("RegisterImage not yet implemented")
}

func (s *NATSImageService) DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error) {
	return nil, errors.New("DeregisterImage not yet implemented")
}

func (s *NATSImageService) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error) {
	return nil, errors.New("ModifyImageAttribute not yet implemented")
}

func (s *NATSImageService) ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error) {
	return nil, errors.New("ResetImageAttribute not yet implemented")
}
