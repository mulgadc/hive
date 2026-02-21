package handlers_ec2_vpc

import "github.com/aws/aws-sdk-go/service/ec2"

// VPCService defines the interface for VPC and Subnet operations
type VPCService interface {
	CreateVpc(input *ec2.CreateVpcInput) (*ec2.CreateVpcOutput, error)
	DeleteVpc(input *ec2.DeleteVpcInput) (*ec2.DeleteVpcOutput, error)
	DescribeVpcs(input *ec2.DescribeVpcsInput) (*ec2.DescribeVpcsOutput, error)
	CreateSubnet(input *ec2.CreateSubnetInput) (*ec2.CreateSubnetOutput, error)
	DeleteSubnet(input *ec2.DeleteSubnetInput) (*ec2.DeleteSubnetOutput, error)
	DescribeSubnets(input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error)
}
