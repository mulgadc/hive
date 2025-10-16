package handlers_ec2_image

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// MockImageService provides mock responses for testing
type MockImageService struct{}

// NewMockImageService creates a new mock image service
func NewMockImageService() ImageService {
	return &MockImageService{}
}

func (s *MockImageService) CreateImage(input *ec2.CreateImageInput) (*ec2.CreateImageOutput, error) {
	return &ec2.CreateImageOutput{
		ImageId: aws.String("ami-0123456789abcdef0"),
	}, nil
}

func (s *MockImageService) CopyImage(input *ec2.CopyImageInput) (*ec2.CopyImageOutput, error) {
	return &ec2.CopyImageOutput{
		ImageId: aws.String("ami-0987654321fedcba0"),
	}, nil
}

func (s *MockImageService) DescribeImages(input *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
	return &ec2.DescribeImagesOutput{
		Images: []*ec2.Image{
			{
				ImageId:      aws.String("ami-0123456789abcdef0"),
				Name:         aws.String("test-image"),
				State:        aws.String("available"),
				Architecture: aws.String("x86_64"),
				ImageType:    aws.String("machine"),
				OwnerId:      aws.String("123456789012"),
				Public:       aws.Bool(false),
			},
		},
	}, nil
}

func (s *MockImageService) DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (*ec2.DescribeImageAttributeOutput, error) {
	return &ec2.DescribeImageAttributeOutput{
		ImageId: input.ImageId,
		Description: &ec2.AttributeValue{
			Value: aws.String("Test image description"),
		},
	}, nil
}

func (s *MockImageService) RegisterImage(input *ec2.RegisterImageInput) (*ec2.RegisterImageOutput, error) {
	return &ec2.RegisterImageOutput{
		ImageId: aws.String("ami-0123456789abcdef0"),
	}, nil
}

func (s *MockImageService) DeregisterImage(input *ec2.DeregisterImageInput) (*ec2.DeregisterImageOutput, error) {
	return &ec2.DeregisterImageOutput{}, nil
}

func (s *MockImageService) ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (*ec2.ModifyImageAttributeOutput, error) {
	return &ec2.ModifyImageAttributeOutput{}, nil
}

func (s *MockImageService) ResetImageAttribute(input *ec2.ResetImageAttributeInput) (*ec2.ResetImageAttributeOutput, error) {
	return &ec2.ResetImageAttributeOutput{}, nil
}
