package handlers_ec2_image

import "github.com/aws/aws-sdk-go/service/ec2"

// ImageService defines the interface for EC2 image operations business logic
type ImageService interface {
	CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error)
	CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error)
	DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error)
	DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error)
	RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error)
	DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error)
	ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error)
	ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error)
}
