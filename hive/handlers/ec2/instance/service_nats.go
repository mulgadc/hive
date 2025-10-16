package handlers_ec2_instance

import (
	"github.com/aws/aws-sdk-go/service/ec2"
)

// NATSInstanceService handles instance operations via NATS messaging
// This will be implemented in Phase 2
type NATSInstanceService struct {
	// natsConn *nats.Conn - will be added in Phase 2
}

// NewNATSInstanceService creates a new NATS-based instance service
// func NewNATSInstanceService(conn *nats.Conn) InstanceService {
// 	return &NATSInstanceService{natsConn: conn}
// }

// TODO Phase 2: Implement NATS-based operations
// These will publish requests to NATS topics and wait for responses

func (s *NATSInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
	panic("NATS service not yet implemented - use MockInstanceService for testing")
}
