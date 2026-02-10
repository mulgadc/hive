package handlers_ec2_eigw

import "github.com/aws/aws-sdk-go/service/ec2"

// EgressOnlyIGWService defines the interface for Egress-only Internet Gateway operations
type EgressOnlyIGWService interface {
	CreateEgressOnlyInternetGateway(input *ec2.CreateEgressOnlyInternetGatewayInput) (*ec2.CreateEgressOnlyInternetGatewayOutput, error)
	DeleteEgressOnlyInternetGateway(input *ec2.DeleteEgressOnlyInternetGatewayInput) (*ec2.DeleteEgressOnlyInternetGatewayOutput, error)
	DescribeEgressOnlyInternetGateways(input *ec2.DescribeEgressOnlyInternetGatewaysInput) (*ec2.DescribeEgressOnlyInternetGatewaysOutput, error)
}
