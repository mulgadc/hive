package handlers_ec2_tags

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSTagsService handles tag operations via NATS messaging
type NATSTagsService struct {
	natsConn *nats.Conn
}

// NewNATSTagsService creates a new NATS-based tags service
func NewNATSTagsService(conn *nats.Conn) TagsService {
	return &NATSTagsService{natsConn: conn}
}

func (s *NATSTagsService) CreateTags(input *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	return utils.NATSRequest[ec2.CreateTagsOutput](s.natsConn, "ec2.CreateTags", input, 30*time.Second)
}

func (s *NATSTagsService) DescribeTags(input *ec2.DescribeTagsInput) (*ec2.DescribeTagsOutput, error) {
	return utils.NATSRequest[ec2.DescribeTagsOutput](s.natsConn, "ec2.DescribeTags", input, 30*time.Second)
}

func (s *NATSTagsService) DeleteTags(input *ec2.DeleteTagsInput) (*ec2.DeleteTagsOutput, error) {
	return utils.NATSRequest[ec2.DeleteTagsOutput](s.natsConn, "ec2.DeleteTags", input, 30*time.Second)
}
