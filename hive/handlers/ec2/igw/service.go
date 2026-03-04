package handlers_ec2_igw

import "github.com/aws/aws-sdk-go/service/ec2"

// IGWService defines the interface for Internet Gateway operations
type IGWService interface {
	CreateInternetGateway(input *ec2.CreateInternetGatewayInput, accountID string) (*ec2.CreateInternetGatewayOutput, error)
	DeleteInternetGateway(input *ec2.DeleteInternetGatewayInput, accountID string) (*ec2.DeleteInternetGatewayOutput, error)
	DescribeInternetGateways(input *ec2.DescribeInternetGatewaysInput, accountID string) (*ec2.DescribeInternetGatewaysOutput, error)
	AttachInternetGateway(input *ec2.AttachInternetGatewayInput, accountID string) (*ec2.AttachInternetGatewayOutput, error)
	DetachInternetGateway(input *ec2.DetachInternetGatewayInput, accountID string) (*ec2.DetachInternetGatewayOutput, error)
}
