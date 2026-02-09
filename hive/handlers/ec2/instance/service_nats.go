package handlers_ec2_instance

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSInstanceService handles instance operations via NATS messaging
type NATSInstanceService struct {
	natsConn *nats.Conn
}

// NewNATSInstanceService creates a new NATS-based instance service
func NewNATSInstanceService(conn *nats.Conn) InstanceService {
	return &NATSInstanceService{natsConn: conn}
}

func (s *NATSInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
	return utils.NATSRequest[ec2.Reservation](s.natsConn, "ec2.RunInstances", input, 5*time.Minute)
}
