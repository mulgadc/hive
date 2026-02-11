package handlers_ec2_eigw

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSEgressOnlyIGWService handles Egress-only Internet Gateway operations via NATS messaging
type NATSEgressOnlyIGWService struct {
	natsConn *nats.Conn
}

// NewNATSEgressOnlyIGWService creates a new NATS-based Egress-only Internet Gateway service
func NewNATSEgressOnlyIGWService(conn *nats.Conn) EgressOnlyIGWService {
	return &NATSEgressOnlyIGWService{natsConn: conn}
}

func (s *NATSEgressOnlyIGWService) CreateEgressOnlyInternetGateway(input *ec2.CreateEgressOnlyInternetGatewayInput) (*ec2.CreateEgressOnlyInternetGatewayOutput, error) {
	return utils.NATSRequest[ec2.CreateEgressOnlyInternetGatewayOutput](s.natsConn, "ec2.CreateEgressOnlyInternetGateway", input, 30*time.Second)
}

func (s *NATSEgressOnlyIGWService) DeleteEgressOnlyInternetGateway(input *ec2.DeleteEgressOnlyInternetGatewayInput) (*ec2.DeleteEgressOnlyInternetGatewayOutput, error) {
	return utils.NATSRequest[ec2.DeleteEgressOnlyInternetGatewayOutput](s.natsConn, "ec2.DeleteEgressOnlyInternetGateway", input, 30*time.Second)
}

func (s *NATSEgressOnlyIGWService) DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error) {
	return utils.NATSRequest[ec2.DescribeEgressOnlyInternetGatewaysOutput](s.natsConn, "ec2.DescribeEgressOnlyInternetGateways", input, 30*time.Second)
}
