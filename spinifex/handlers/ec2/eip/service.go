package handlers_ec2_eip

import "github.com/aws/aws-sdk-go/service/ec2"

// EIPService defines the interface for Elastic IP address operations.
type EIPService interface {
	AllocateAddress(input *ec2.AllocateAddressInput, accountID string) (*ec2.AllocateAddressOutput, error)
	ReleaseAddress(input *ec2.ReleaseAddressInput, accountID string) (*ec2.ReleaseAddressOutput, error)
	AssociateAddress(input *ec2.AssociateAddressInput, accountID string) (*ec2.AssociateAddressOutput, error)
	DisassociateAddress(input *ec2.DisassociateAddressInput, accountID string) (*ec2.DisassociateAddressOutput, error)
	DescribeAddresses(input *ec2.DescribeAddressesInput, accountID string) (*ec2.DescribeAddressesOutput, error)
	DescribeAddressesAttribute(input *ec2.DescribeAddressesAttributeInput, accountID string) (*ec2.DescribeAddressesAttributeOutput, error)
}
