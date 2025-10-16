package handlers_ec2_key

import (
	"github.com/aws/aws-sdk-go/service/ec2"
)

// NATSKeyService handles key operations via NATS messaging
// This will be implemented in Phase 2
type NATSKeyService struct {
	// natsConn *nats.Conn - will be added in Phase 2
}

// NewNATSKeyService creates a new NATS-based key service
// func NewNATSKeyService(conn *nats.Conn) KeyService {
// 	return &NATSKeyService{natsConn: conn}
// }

// TODO Phase 2: Implement NATS-based operations
// These will publish requests to NATS topics and wait for responses

func (s *NATSKeyService) CreateKeyPair(input *ec2.CreateKeyPairInput) (*ec2.CreateKeyPairOutput, error) {
	panic("NATS service not yet implemented - use MockKeyService for testing")
}

func (s *NATSKeyService) DeleteKeyPair(input *ec2.DeleteKeyPairInput) (*ec2.DeleteKeyPairOutput, error) {
	panic("NATS service not yet implemented - use MockKeyService for testing")
}

func (s *NATSKeyService) DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (*ec2.DescribeKeyPairsOutput, error) {
	panic("NATS service not yet implemented - use MockKeyService for testing")
}

func (s *NATSKeyService) ImportKeyPair(input *ec2.ImportKeyPairInput) (*ec2.ImportKeyPairOutput, error) {
	panic("NATS service not yet implemented - use MockKeyService for testing")
}
