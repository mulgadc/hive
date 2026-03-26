package handlers_elbv2

import (
	"time"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

const defaultTimeout = 30 * time.Second

// NATSELBv2Service handles ELBv2 operations via NATS messaging.
type NATSELBv2Service struct {
	natsConn *nats.Conn
}

// NewNATSELBv2Service creates a new NATS-based ELBv2 service.
func NewNATSELBv2Service(conn *nats.Conn) ELBv2Service {
	return &NATSELBv2Service{natsConn: conn}
}

func (s *NATSELBv2Service) CreateLoadBalancer(input *elbv2.CreateLoadBalancerInput, accountID string) (*elbv2.CreateLoadBalancerOutput, error) {
	return utils.NATSRequest[elbv2.CreateLoadBalancerOutput](s.natsConn, "elbv2.CreateLoadBalancer", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DeleteLoadBalancer(input *elbv2.DeleteLoadBalancerInput, accountID string) (*elbv2.DeleteLoadBalancerOutput, error) {
	return utils.NATSRequest[elbv2.DeleteLoadBalancerOutput](s.natsConn, "elbv2.DeleteLoadBalancer", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DescribeLoadBalancers(input *elbv2.DescribeLoadBalancersInput, accountID string) (*elbv2.DescribeLoadBalancersOutput, error) {
	return utils.NATSRequest[elbv2.DescribeLoadBalancersOutput](s.natsConn, "elbv2.DescribeLoadBalancers", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) CreateTargetGroup(input *elbv2.CreateTargetGroupInput, accountID string) (*elbv2.CreateTargetGroupOutput, error) {
	return utils.NATSRequest[elbv2.CreateTargetGroupOutput](s.natsConn, "elbv2.CreateTargetGroup", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DeleteTargetGroup(input *elbv2.DeleteTargetGroupInput, accountID string) (*elbv2.DeleteTargetGroupOutput, error) {
	return utils.NATSRequest[elbv2.DeleteTargetGroupOutput](s.natsConn, "elbv2.DeleteTargetGroup", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DescribeTargetGroups(input *elbv2.DescribeTargetGroupsInput, accountID string) (*elbv2.DescribeTargetGroupsOutput, error) {
	return utils.NATSRequest[elbv2.DescribeTargetGroupsOutput](s.natsConn, "elbv2.DescribeTargetGroups", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) RegisterTargets(input *elbv2.RegisterTargetsInput, accountID string) (*elbv2.RegisterTargetsOutput, error) {
	return utils.NATSRequest[elbv2.RegisterTargetsOutput](s.natsConn, "elbv2.RegisterTargets", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DeregisterTargets(input *elbv2.DeregisterTargetsInput, accountID string) (*elbv2.DeregisterTargetsOutput, error) {
	return utils.NATSRequest[elbv2.DeregisterTargetsOutput](s.natsConn, "elbv2.DeregisterTargets", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DescribeTargetHealth(input *elbv2.DescribeTargetHealthInput, accountID string) (*elbv2.DescribeTargetHealthOutput, error) {
	return utils.NATSRequest[elbv2.DescribeTargetHealthOutput](s.natsConn, "elbv2.DescribeTargetHealth", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) CreateListener(input *elbv2.CreateListenerInput, accountID string) (*elbv2.CreateListenerOutput, error) {
	return utils.NATSRequest[elbv2.CreateListenerOutput](s.natsConn, "elbv2.CreateListener", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DeleteListener(input *elbv2.DeleteListenerInput, accountID string) (*elbv2.DeleteListenerOutput, error) {
	return utils.NATSRequest[elbv2.DeleteListenerOutput](s.natsConn, "elbv2.DeleteListener", input, defaultTimeout, accountID)
}

func (s *NATSELBv2Service) DescribeListeners(input *elbv2.DescribeListenersInput, accountID string) (*elbv2.DescribeListenersOutput, error) {
	return utils.NATSRequest[elbv2.DescribeListenersOutput](s.natsConn, "elbv2.DescribeListeners", input, defaultTimeout, accountID)
}
