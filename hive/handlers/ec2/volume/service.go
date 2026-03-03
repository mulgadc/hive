package handlers_ec2_volume

import "github.com/aws/aws-sdk-go/service/ec2"

// VolumeService defines the interface for EBS volume operations
type VolumeService interface {
	CreateVolume(accountID string, input *ec2.CreateVolumeInput) (*ec2.Volume, error)
	DescribeVolumes(accountID string, input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error)
	ModifyVolume(accountID string, input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error)
	DeleteVolume(accountID string, input *ec2.DeleteVolumeInput) (*ec2.DeleteVolumeOutput, error)
	DescribeVolumeStatus(accountID string, input *ec2.DescribeVolumeStatusInput) (*ec2.DescribeVolumeStatusOutput, error)
}
