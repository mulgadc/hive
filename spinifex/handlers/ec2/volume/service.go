package handlers_ec2_volume

import "github.com/aws/aws-sdk-go/service/ec2"

// VolumeService defines the interface for EBS volume operations
type VolumeService interface {
	CreateVolume(input *ec2.CreateVolumeInput, accountID string) (*ec2.Volume, error)
	DescribeVolumes(input *ec2.DescribeVolumesInput, accountID string) (*ec2.DescribeVolumesOutput, error)
	ModifyVolume(input *ec2.ModifyVolumeInput, accountID string) (*ec2.ModifyVolumeOutput, error)
	DeleteVolume(input *ec2.DeleteVolumeInput, accountID string) (*ec2.DeleteVolumeOutput, error)
	DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput, accountID string) (*ec2.DescribeVolumeStatusOutput, error)
	DescribeVolumesModifications(input *ec2.DescribeVolumesModificationsInput, accountID string) (*ec2.DescribeVolumesModificationsOutput, error)
}
