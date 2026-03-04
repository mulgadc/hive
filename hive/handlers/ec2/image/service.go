package handlers_ec2_image

import "github.com/aws/aws-sdk-go/service/ec2"

// ImageService defines the interface for EC2 image operations business logic
type ImageService interface {
	CreateImage(input *ec2.CreateImageInput, accountID string) (*ec2.CreateImageOutput, error)
	CopyImage(input *ec2.CopyImageInput, accountID string) (*ec2.CopyImageOutput, error)
	DescribeImages(input *ec2.DescribeImagesInput, accountID string) (*ec2.DescribeImagesOutput, error)
	DescribeImageAttribute(input *ec2.DescribeImageAttributeInput, accountID string) (*ec2.DescribeImageAttributeOutput, error)
	RegisterImage(input *ec2.RegisterImageInput, accountID string) (*ec2.RegisterImageOutput, error)
	DeregisterImage(input *ec2.DeregisterImageInput, accountID string) (*ec2.DeregisterImageOutput, error)
	ModifyImageAttribute(input *ec2.ModifyImageAttributeInput, accountID string) (*ec2.ModifyImageAttributeOutput, error)
	ResetImageAttribute(input *ec2.ResetImageAttributeInput, accountID string) (*ec2.ResetImageAttributeOutput, error)
}
