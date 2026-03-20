package handlers_ec2_placementgroup

import "github.com/aws/aws-sdk-go/service/ec2"

// PlacementGroupService defines the interface for placement group operations.
type PlacementGroupService interface {
	CreatePlacementGroup(input *ec2.CreatePlacementGroupInput, accountID string) (*ec2.CreatePlacementGroupOutput, error)
	DeletePlacementGroup(input *ec2.DeletePlacementGroupInput, accountID string) (*ec2.DeletePlacementGroupOutput, error)
	DescribePlacementGroups(input *ec2.DescribePlacementGroupsInput, accountID string) (*ec2.DescribePlacementGroupsOutput, error)
	ReserveSpreadNodes(input *ReserveSpreadNodesInput, accountID string) (*ReserveSpreadNodesOutput, error)
	FinalizeSpreadInstances(input *FinalizeSpreadInstancesInput, accountID string) (*FinalizeSpreadInstancesOutput, error)
	ReleaseSpreadNodes(input *ReleaseSpreadNodesInput, accountID string) (*ReleaseSpreadNodesOutput, error)
	RemoveInstance(input *RemoveInstanceInput, accountID string) (*RemoveInstanceOutput, error)
}

// ReserveSpreadNodesInput requests atomic node reservation for a spread placement group.
type ReserveSpreadNodesInput struct {
	GroupName     string   `json:"group_name"`
	EligibleNodes []string `json:"eligible_nodes"` // nodes with capacity (from fan-out)
	MinCount      int      `json:"min_count"`
	MaxCount      int      `json:"max_count"`
}

// ReserveSpreadNodesOutput contains the nodes selected for launch.
type ReserveSpreadNodesOutput struct {
	ReservedNodes []string `json:"reserved_nodes"`
}

// FinalizeSpreadInstancesInput records actual instance IDs on reserved nodes.
type FinalizeSpreadInstancesInput struct {
	GroupName     string              `json:"group_name"`
	NodeInstances map[string][]string `json:"node_instances"` // node -> instance IDs
}

// FinalizeSpreadInstancesOutput is empty on success.
type FinalizeSpreadInstancesOutput struct{}

// ReleaseSpreadNodesInput releases previously reserved node slots (rollback).
type ReleaseSpreadNodesInput struct {
	GroupName string   `json:"group_name"`
	Nodes     []string `json:"nodes"`
}

// ReleaseSpreadNodesOutput is empty on success.
type ReleaseSpreadNodesOutput struct{}

// RemoveInstanceInput removes a specific instance from its placement group's NodeInstances.
type RemoveInstanceInput struct {
	GroupName  string `json:"group_name"`
	NodeName   string `json:"node_name"`
	InstanceID string `json:"instance_id"`
}

// RemoveInstanceOutput is empty on success.
type RemoveInstanceOutput struct{}
