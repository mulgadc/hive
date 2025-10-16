package handlers_ec2_key

import "github.com/aws/aws-sdk-go/service/ec2"

// KeyService defines the interface for EC2 key pair operations business logic
type KeyService interface {
	CreateKeyPair(input *ec2.CreateKeyPairInput) (*ec2.CreateKeyPairOutput, error)
	DeleteKeyPair(input *ec2.DeleteKeyPairInput) (*ec2.DeleteKeyPairOutput, error)
	DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (*ec2.DescribeKeyPairsOutput, error)
	ImportKeyPair(input *ec2.ImportKeyPairInput) (*ec2.ImportKeyPairOutput, error)
}
