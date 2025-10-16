package handlers_ec2_instance

import "github.com/aws/aws-sdk-go/service/ec2"

// InstanceService defines the interface for EC2 instance operations business logic
type InstanceService interface {
	RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error)
}
