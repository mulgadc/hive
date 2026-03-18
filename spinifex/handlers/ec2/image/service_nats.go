package handlers_ec2_image

import (
	"errors"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// NATSImageService handles image operations via NATS messaging
type NATSImageService struct {
	natsConn      *nats.Conn
	expectedNodes int
}

// NewNATSImageService creates a new NATS-based image service.
// expectedNodes is used by scatter-gather operations (e.g. CreateImage) to
// enable early exit once all nodes have responded.
func NewNATSImageService(conn *nats.Conn, expectedNodes int) ImageService {
	return &NATSImageService{natsConn: conn, expectedNodes: expectedNodes}
}

func (s *NATSImageService) DescribeImages(input *ec2.DescribeImagesInput, accountID string) (*ec2.DescribeImagesOutput, error) {
	return utils.NATSRequest[ec2.DescribeImagesOutput](s.natsConn, "ec2.DescribeImages", input, 30*time.Second, accountID)
}

func (s *NATSImageService) CreateImage(input *ec2.CreateImageInput, accountID string) (*ec2.CreateImageOutput, error) {
	return utils.NATSScatterGather[ec2.CreateImageOutput](s.natsConn, "ec2.CreateImage", input, 120*time.Second, s.expectedNodes, accountID)
}

func (s *NATSImageService) CopyImage(input *ec2.CopyImageInput, accountID string) (*ec2.CopyImageOutput, error) {
	return nil, errors.New("CopyImage not yet implemented")
}

func (s *NATSImageService) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput, accountID string) (*ec2.DescribeImageAttributeOutput, error) {
	return nil, errors.New("DescribeImageAttribute not yet implemented")
}

func (s *NATSImageService) RegisterImage(input *ec2.RegisterImageInput, accountID string) (*ec2.RegisterImageOutput, error) {
	return nil, errors.New("RegisterImage not yet implemented")
}

func (s *NATSImageService) DeregisterImage(input *ec2.DeregisterImageInput, accountID string) (*ec2.DeregisterImageOutput, error) {
	return nil, errors.New("DeregisterImage not yet implemented")
}

func (s *NATSImageService) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput, accountID string) (*ec2.ModifyImageAttributeOutput, error) {
	return nil, errors.New("ModifyImageAttribute not yet implemented")
}

func (s *NATSImageService) ResetImageAttribute(input *ec2.ResetImageAttributeInput, accountID string) (*ec2.ResetImageAttributeOutput, error) {
	return nil, errors.New("ResetImageAttribute not yet implemented")
}
