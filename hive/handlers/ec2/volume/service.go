package handlers_ec2_volume

import "github.com/aws/aws-sdk-go/service/ec2"

// VolumeService defines the interface for EBS volume operations
type VolumeService interface {
	DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error)
}
