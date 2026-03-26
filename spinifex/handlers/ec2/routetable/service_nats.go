package handlers_ec2_routetable

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// NATSRouteTableService handles Route Table operations via NATS messaging
type NATSRouteTableService struct {
	natsConn *nats.Conn
}

// NewNATSRouteTableService creates a new NATS-based Route Table service
func NewNATSRouteTableService(conn *nats.Conn) RouteTableService {
	return &NATSRouteTableService{natsConn: conn}
}

func (s *NATSRouteTableService) CreateRouteTable(input *ec2.CreateRouteTableInput, accountID string) (*ec2.CreateRouteTableOutput, error) {
	return utils.NATSRequest[ec2.CreateRouteTableOutput](s.natsConn, "ec2.CreateRouteTable", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) DeleteRouteTable(input *ec2.DeleteRouteTableInput, accountID string) (*ec2.DeleteRouteTableOutput, error) {
	return utils.NATSRequest[ec2.DeleteRouteTableOutput](s.natsConn, "ec2.DeleteRouteTable", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) DescribeRouteTables(input *ec2.DescribeRouteTablesInput, accountID string) (*ec2.DescribeRouteTablesOutput, error) {
	return utils.NATSRequest[ec2.DescribeRouteTablesOutput](s.natsConn, "ec2.DescribeRouteTables", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) CreateRoute(input *ec2.CreateRouteInput, accountID string) (*ec2.CreateRouteOutput, error) {
	return utils.NATSRequest[ec2.CreateRouteOutput](s.natsConn, "ec2.CreateRoute", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) DeleteRoute(input *ec2.DeleteRouteInput, accountID string) (*ec2.DeleteRouteOutput, error) {
	return utils.NATSRequest[ec2.DeleteRouteOutput](s.natsConn, "ec2.DeleteRoute", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) ReplaceRoute(input *ec2.ReplaceRouteInput, accountID string) (*ec2.ReplaceRouteOutput, error) {
	return utils.NATSRequest[ec2.ReplaceRouteOutput](s.natsConn, "ec2.ReplaceRoute", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) AssociateRouteTable(input *ec2.AssociateRouteTableInput, accountID string) (*ec2.AssociateRouteTableOutput, error) {
	return utils.NATSRequest[ec2.AssociateRouteTableOutput](s.natsConn, "ec2.AssociateRouteTable", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) DisassociateRouteTable(input *ec2.DisassociateRouteTableInput, accountID string) (*ec2.DisassociateRouteTableOutput, error) {
	return utils.NATSRequest[ec2.DisassociateRouteTableOutput](s.natsConn, "ec2.DisassociateRouteTable", input, 30*time.Second, accountID)
}

func (s *NATSRouteTableService) ReplaceRouteTableAssociation(input *ec2.ReplaceRouteTableAssociationInput, accountID string) (*ec2.ReplaceRouteTableAssociationOutput, error) {
	return utils.NATSRequest[ec2.ReplaceRouteTableAssociationOutput](s.natsConn, "ec2.ReplaceRouteTableAssociation", input, 30*time.Second, accountID)
}
