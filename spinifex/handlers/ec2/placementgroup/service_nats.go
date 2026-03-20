package handlers_ec2_placementgroup

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// NATSPlacementGroupService handles placement group operations via NATS messaging.
type NATSPlacementGroupService struct {
	natsConn *nats.Conn
}

// NewNATSPlacementGroupService creates a new NATS-based placement group service.
func NewNATSPlacementGroupService(conn *nats.Conn) PlacementGroupService {
	return &NATSPlacementGroupService{natsConn: conn}
}

func (s *NATSPlacementGroupService) CreatePlacementGroup(input *ec2.CreatePlacementGroupInput, accountID string) (*ec2.CreatePlacementGroupOutput, error) {
	return utils.NATSRequest[ec2.CreatePlacementGroupOutput](s.natsConn, "ec2.CreatePlacementGroup", input, 30*time.Second, accountID)
}

func (s *NATSPlacementGroupService) DeletePlacementGroup(input *ec2.DeletePlacementGroupInput, accountID string) (*ec2.DeletePlacementGroupOutput, error) {
	return utils.NATSRequest[ec2.DeletePlacementGroupOutput](s.natsConn, "ec2.DeletePlacementGroup", input, 30*time.Second, accountID)
}

func (s *NATSPlacementGroupService) DescribePlacementGroups(input *ec2.DescribePlacementGroupsInput, accountID string) (*ec2.DescribePlacementGroupsOutput, error) {
	return utils.NATSRequest[ec2.DescribePlacementGroupsOutput](s.natsConn, "ec2.DescribePlacementGroups", input, 30*time.Second, accountID)
}

func (s *NATSPlacementGroupService) ReserveSpreadNodes(input *ReserveSpreadNodesInput, accountID string) (*ReserveSpreadNodesOutput, error) {
	return utils.NATSRequest[ReserveSpreadNodesOutput](s.natsConn, "ec2.ReserveSpreadNodes", input, 30*time.Second, accountID)
}

func (s *NATSPlacementGroupService) FinalizeSpreadInstances(input *FinalizeSpreadInstancesInput, accountID string) (*FinalizeSpreadInstancesOutput, error) {
	return utils.NATSRequest[FinalizeSpreadInstancesOutput](s.natsConn, "ec2.FinalizeSpreadInstances", input, 30*time.Second, accountID)
}

func (s *NATSPlacementGroupService) ReleaseSpreadNodes(input *ReleaseSpreadNodesInput, accountID string) (*ReleaseSpreadNodesOutput, error) {
	return utils.NATSRequest[ReleaseSpreadNodesOutput](s.natsConn, "ec2.ReleaseSpreadNodes", input, 30*time.Second, accountID)
}
