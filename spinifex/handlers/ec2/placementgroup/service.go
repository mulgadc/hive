package handlers_ec2_placementgroup

import "github.com/aws/aws-sdk-go/service/ec2"

// PlacementGroupService defines the interface for placement group operations.
type PlacementGroupService interface {
	CreatePlacementGroup(input *ec2.CreatePlacementGroupInput, accountID string) (*ec2.CreatePlacementGroupOutput, error)
	DeletePlacementGroup(input *ec2.DeletePlacementGroupInput, accountID string) (*ec2.DeletePlacementGroupOutput, error)
	DescribePlacementGroups(input *ec2.DescribePlacementGroupsInput, accountID string) (*ec2.DescribePlacementGroupsOutput, error)
}
