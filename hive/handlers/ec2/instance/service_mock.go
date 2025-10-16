package handlers_ec2_instance

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// MockInstanceService provides mock responses for testing
type MockInstanceService struct{}

// NewMockInstanceService creates a new mock instance service
func NewMockInstanceService() InstanceService {
	return &MockInstanceService{}
}

func (s *MockInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
	instance := &ec2.Instance{
		InstanceId: aws.String("i-0123456789abcdef0"),
		State: &ec2.InstanceState{
			Code: aws.Int64(16),
			Name: aws.String("running"),
		},
		ImageId:      input.ImageId,
		InstanceType: input.InstanceType,
		KeyName:      input.KeyName,
		SubnetId:     input.SubnetId,
	}

	reservation := &ec2.Reservation{
		Instances: []*ec2.Instance{instance},
		OwnerId:   aws.String("123456789012"),
	}

	return reservation, nil
}
