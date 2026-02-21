package handlers_ec2_vpc

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSVPCService handles VPC and Subnet operations via NATS messaging
type NATSVPCService struct {
	natsConn *nats.Conn
}

// NewNATSVPCService creates a new NATS-based VPC service
func NewNATSVPCService(conn *nats.Conn) VPCService {
	return &NATSVPCService{natsConn: conn}
}

func (s *NATSVPCService) CreateVpc(input *ec2.CreateVpcInput) (*ec2.CreateVpcOutput, error) {
	return utils.NATSRequest[ec2.CreateVpcOutput](s.natsConn, "ec2.CreateVpc", input, 30*time.Second)
}

func (s *NATSVPCService) DeleteVpc(input *ec2.DeleteVpcInput) (*ec2.DeleteVpcOutput, error) {
	return utils.NATSRequest[ec2.DeleteVpcOutput](s.natsConn, "ec2.DeleteVpc", input, 30*time.Second)
}

func (s *NATSVPCService) DescribeVpcs(input *ec2.DescribeVpcsInput) (*ec2.DescribeVpcsOutput, error) {
	return utils.NATSRequest[ec2.DescribeVpcsOutput](s.natsConn, "ec2.DescribeVpcs", input, 30*time.Second)
}

func (s *NATSVPCService) CreateSubnet(input *ec2.CreateSubnetInput) (*ec2.CreateSubnetOutput, error) {
	return utils.NATSRequest[ec2.CreateSubnetOutput](s.natsConn, "ec2.CreateSubnet", input, 30*time.Second)
}

func (s *NATSVPCService) DeleteSubnet(input *ec2.DeleteSubnetInput) (*ec2.DeleteSubnetOutput, error) {
	return utils.NATSRequest[ec2.DeleteSubnetOutput](s.natsConn, "ec2.DeleteSubnet", input, 30*time.Second)
}

func (s *NATSVPCService) DescribeSubnets(input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
	return utils.NATSRequest[ec2.DescribeSubnetsOutput](s.natsConn, "ec2.DescribeSubnets", input, 30*time.Second)
}
