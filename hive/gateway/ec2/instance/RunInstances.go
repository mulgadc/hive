package gateway_ec2_instance

import (
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"

	handlers_ec2_instance "github.com/mulgadc/hive/hive/handlers/ec2/instance"
	"github.com/nats-io/nats.go"
)

type RunInstancesResponse struct {
	Reservation *ec2.Reservation `locationName:"RunInstancesResponse"`
}

func ValidateRunInstancesInput(input *ec2.RunInstancesInput) (err error) {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.MinCount == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if *input.MinCount == 0 {
		return awserrors.NewErrorf(awserrors.ErrorInvalidParameterValue,
			"Value (%d) for parameter minCount is invalid. Expected a positive integer.", *input.MinCount)
	}

	if *input.MaxCount == 0 {
		return awserrors.NewErrorf(awserrors.ErrorInvalidParameterValue,
			"Value (%d) for parameter maxCount is invalid. Expected a positive integer.", *input.MaxCount)
	}

	// Additional validation from EC2 spec
	if *input.MinCount > *input.MaxCount {
		return awserrors.NewErrorf(awserrors.ErrorInvalidParameterValue,
			"Value (%d) for parameter minCount is invalid. minCount may not exceed maxCount (%d).",
			*input.MinCount, *input.MaxCount)
	}

	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.InstanceType == nil || *input.InstanceType == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return awserrors.NewErrorf(awserrors.ErrorInvalidAMIIDMalformed,
			"Invalid id: %q (expecting \"ami-...\")", *input.ImageId)
	}

	return
}

func RunInstances(input *ec2.RunInstancesInput, natsConn *nats.Conn) (reservation ec2.Reservation, err error) {

	// Validate input
	err = ValidateRunInstancesInput(input)

	if err != nil {
		return reservation, err
	}

	// Create NATS-based instance service
	service := handlers_ec2_instance.NewNATSInstanceService(natsConn)

	// Call the service directly (no need for JSON marshaling/unmarshaling in same process)
	reservationPtr, err := service.RunInstances(input)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			// ErrNoResponders means no daemon subscribes to ec2.RunInstances.{type}.
			// This happens when either: (a) the type is unknown, or (b) all nodes
			// are at capacity. Query DescribeInstanceTypes to differentiate.
			if !isKnownInstanceType(natsConn, *input.InstanceType) {
				return reservation, errors.New(awserrors.ErrorInvalidInstanceType)
			}
			return reservation, errors.New(awserrors.ErrorInsufficientInstanceCapacity)
		}
		return reservation, err
	}

	// Dereference pointer to return value
	return *reservationPtr, nil
}

// isKnownInstanceType checks whether any daemon recognizes the given instance type.
func isKnownInstanceType(natsConn *nats.Conn, instanceType string) bool {
	result, err := utils.NATSRequest[ec2.DescribeInstanceTypesOutput](
		natsConn, "ec2.DescribeInstanceTypes", &ec2.DescribeInstanceTypesInput{}, 3*time.Second)
	if err != nil || result == nil {
		return false
	}
	for _, t := range result.InstanceTypes {
		if t.InstanceType != nil && *t.InstanceType == instanceType {
			return true
		}
	}
	return false
}
