package handlers_ec2_natgw

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// NATSNatGatewayService handles NAT Gateway operations via NATS messaging
type NATSNatGatewayService struct {
	natsConn *nats.Conn
}

// NewNATSNatGatewayService creates a new NATS-based NAT Gateway service
func NewNATSNatGatewayService(conn *nats.Conn) NatGatewayService {
	return &NATSNatGatewayService{natsConn: conn}
}

func (s *NATSNatGatewayService) CreateNatGateway(input *ec2.CreateNatGatewayInput, accountID string) (*ec2.CreateNatGatewayOutput, error) {
	return utils.NATSRequest[ec2.CreateNatGatewayOutput](s.natsConn, "ec2.CreateNatGateway", input, 30*time.Second, accountID)
}

func (s *NATSNatGatewayService) DeleteNatGateway(input *ec2.DeleteNatGatewayInput, accountID string) (*ec2.DeleteNatGatewayOutput, error) {
	return utils.NATSRequest[ec2.DeleteNatGatewayOutput](s.natsConn, "ec2.DeleteNatGateway", input, 30*time.Second, accountID)
}

func (s *NATSNatGatewayService) DescribeNatGateways(input *ec2.DescribeNatGatewaysInput, accountID string) (*ec2.DescribeNatGatewaysOutput, error) {
	return utils.NATSRequest[ec2.DescribeNatGatewaysOutput](s.natsConn, "ec2.DescribeNatGateways", input, 30*time.Second, accountID)
}
