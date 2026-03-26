package handlers_ec2_routetable

import "github.com/aws/aws-sdk-go/service/ec2"

// RouteTableService defines the interface for Route Table operations
type RouteTableService interface {
	CreateRouteTable(input *ec2.CreateRouteTableInput, accountID string) (*ec2.CreateRouteTableOutput, error)
	DeleteRouteTable(input *ec2.DeleteRouteTableInput, accountID string) (*ec2.DeleteRouteTableOutput, error)
	DescribeRouteTables(input *ec2.DescribeRouteTablesInput, accountID string) (*ec2.DescribeRouteTablesOutput, error)
	CreateRoute(input *ec2.CreateRouteInput, accountID string) (*ec2.CreateRouteOutput, error)
	DeleteRoute(input *ec2.DeleteRouteInput, accountID string) (*ec2.DeleteRouteOutput, error)
	ReplaceRoute(input *ec2.ReplaceRouteInput, accountID string) (*ec2.ReplaceRouteOutput, error)
	AssociateRouteTable(input *ec2.AssociateRouteTableInput, accountID string) (*ec2.AssociateRouteTableOutput, error)
	DisassociateRouteTable(input *ec2.DisassociateRouteTableInput, accountID string) (*ec2.DisassociateRouteTableOutput, error)
	ReplaceRouteTableAssociation(input *ec2.ReplaceRouteTableAssociationInput, accountID string) (*ec2.ReplaceRouteTableAssociationOutput, error)
}
