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

func (s *NATSVPCService) CreateVpc(input *ec2.CreateVpcInput, accountID string) (*ec2.CreateVpcOutput, error) {
	return utils.NATSRequestWithAccount[ec2.CreateVpcOutput](s.natsConn, "ec2.CreateVpc", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DeleteVpc(input *ec2.DeleteVpcInput, accountID string) (*ec2.DeleteVpcOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DeleteVpcOutput](s.natsConn, "ec2.DeleteVpc", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DescribeVpcs(input *ec2.DescribeVpcsInput, accountID string) (*ec2.DescribeVpcsOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DescribeVpcsOutput](s.natsConn, "ec2.DescribeVpcs", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) CreateSubnet(input *ec2.CreateSubnetInput, accountID string) (*ec2.CreateSubnetOutput, error) {
	return utils.NATSRequestWithAccount[ec2.CreateSubnetOutput](s.natsConn, "ec2.CreateSubnet", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DeleteSubnet(input *ec2.DeleteSubnetInput, accountID string) (*ec2.DeleteSubnetOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DeleteSubnetOutput](s.natsConn, "ec2.DeleteSubnet", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DescribeSubnets(input *ec2.DescribeSubnetsInput, accountID string) (*ec2.DescribeSubnetsOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DescribeSubnetsOutput](s.natsConn, "ec2.DescribeSubnets", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) CreateNetworkInterface(input *ec2.CreateNetworkInterfaceInput, accountID string) (*ec2.CreateNetworkInterfaceOutput, error) {
	return utils.NATSRequestWithAccount[ec2.CreateNetworkInterfaceOutput](s.natsConn, "ec2.CreateNetworkInterface", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DeleteNetworkInterface(input *ec2.DeleteNetworkInterfaceInput, accountID string) (*ec2.DeleteNetworkInterfaceOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DeleteNetworkInterfaceOutput](s.natsConn, "ec2.DeleteNetworkInterface", input, 30*time.Second, accountID)
}

func (s *NATSVPCService) DescribeNetworkInterfaces(input *ec2.DescribeNetworkInterfacesInput, accountID string) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return utils.NATSRequestWithAccount[ec2.DescribeNetworkInterfacesOutput](s.natsConn, "ec2.DescribeNetworkInterfaces", input, 30*time.Second, accountID)
}
