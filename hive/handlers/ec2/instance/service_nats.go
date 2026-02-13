package handlers_ec2_instance

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
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
	if input == nil || input.InstanceType == nil {
		return nil, fmt.Errorf("instance type is required")
	}
	topic := fmt.Sprintf("ec2.RunInstances.%s", aws.StringValue(input.InstanceType))
	return utils.NATSRequest[ec2.Reservation](s.natsConn, topic, input, 5*time.Minute)
}
