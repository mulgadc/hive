package handlers_ec2_eip

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// NATSEIPService handles Elastic IP operations via NATS messaging.
type NATSEIPService struct {
	natsConn *nats.Conn
}

// NewNATSEIPService creates a new NATS-based EIP service.
func NewNATSEIPService(conn *nats.Conn) EIPService {
	return &NATSEIPService{natsConn: conn}
}

func (s *NATSEIPService) AllocateAddress(input *ec2.AllocateAddressInput, accountID string) (*ec2.AllocateAddressOutput, error) {
	return utils.NATSRequest[ec2.AllocateAddressOutput](s.natsConn, "ec2.AllocateAddress", input, 30*time.Second, accountID)
}

func (s *NATSEIPService) ReleaseAddress(input *ec2.ReleaseAddressInput, accountID string) (*ec2.ReleaseAddressOutput, error) {
	return utils.NATSRequest[ec2.ReleaseAddressOutput](s.natsConn, "ec2.ReleaseAddress", input, 30*time.Second, accountID)
}

func (s *NATSEIPService) AssociateAddress(input *ec2.AssociateAddressInput, accountID string) (*ec2.AssociateAddressOutput, error) {
	return utils.NATSRequest[ec2.AssociateAddressOutput](s.natsConn, "ec2.AssociateAddress", input, 30*time.Second, accountID)
}

func (s *NATSEIPService) DisassociateAddress(input *ec2.DisassociateAddressInput, accountID string) (*ec2.DisassociateAddressOutput, error) {
	return utils.NATSRequest[ec2.DisassociateAddressOutput](s.natsConn, "ec2.DisassociateAddress", input, 30*time.Second, accountID)
}

func (s *NATSEIPService) DescribeAddresses(input *ec2.DescribeAddressesInput, accountID string) (*ec2.DescribeAddressesOutput, error) {
	return utils.NATSRequest[ec2.DescribeAddressesOutput](s.natsConn, "ec2.DescribeAddresses", input, 30*time.Second, accountID)
}
