package handlers_ec2_key

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSKeyService handles key operations via NATS messaging
type NATSKeyService struct {
	natsConn *nats.Conn
}

// NewNATSKeyService creates a new NATS-based key service
func NewNATSKeyService(conn *nats.Conn) KeyService {
	return &NATSKeyService{natsConn: conn}
}

func (s *NATSKeyService) CreateKeyPair(input *ec2.CreateKeyPairInput) (*ec2.CreateKeyPairOutput, error) {
	return utils.NATSRequest[ec2.CreateKeyPairOutput](s.natsConn, "ec2.CreateKeyPair", input, 30*time.Second)
}

func (s *NATSKeyService) DeleteKeyPair(input *ec2.DeleteKeyPairInput) (*ec2.DeleteKeyPairOutput, error) {
	return utils.NATSRequest[ec2.DeleteKeyPairOutput](s.natsConn, "ec2.DeleteKeyPair", input, 30*time.Second)
}

func (s *NATSKeyService) DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (*ec2.DescribeKeyPairsOutput, error) {
	return utils.NATSRequest[ec2.DescribeKeyPairsOutput](s.natsConn, "ec2.DescribeKeyPairs", input, 30*time.Second)
}

func (s *NATSKeyService) ImportKeyPair(input *ec2.ImportKeyPairInput) (*ec2.ImportKeyPairOutput, error) {
	return utils.NATSRequest[ec2.ImportKeyPairOutput](s.natsConn, "ec2.ImportKeyPair", input, 30*time.Second)
}
