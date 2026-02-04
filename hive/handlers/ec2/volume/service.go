package handlers_ec2_volume

import "github.com/aws/aws-sdk-go/service/ec2"

// VolumeService defines the interface for EBS volume operations
type VolumeService interface {
	CreateVolume(input *ec2.CreateVolumeInput) (*ec2.Volume, error)
	DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error)
	ModifyVolume(input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error)
	DeleteVolume(input *ec2.DeleteVolumeInput) (*ec2.DeleteVolumeOutput, error)
	DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput) (*ec2.DescribeVolumeStatusOutput, error)
}
