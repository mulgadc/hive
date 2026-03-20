package gateway_ec2_instance

import (
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/utils"

	handlers_ec2_instance "github.com/mulgadc/spinifex/spinifex/handlers/ec2/instance"
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
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.MaxCount == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if *input.MaxCount == 0 {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Additional validation from EC2 spec
	if *input.MinCount > *input.MaxCount {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.InstanceType == nil || *input.InstanceType == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return errors.New(awserrors.ErrorInvalidAMIIDMalformed)
	}

	return err
}

func RunInstances(input *ec2.RunInstancesInput, natsConn *nats.Conn, accountID string) (reservation ec2.Reservation, err error) {
	// Validate input
	err = ValidateRunInstancesInput(input)

	if err != nil {
		return reservation, err
	}

	// Multi-node routing: when count > 1 (and no placement group), use the
	// distributeInstances path which fans out capacity queries and launches
	// instances across multiple nodes for best-effort spread.
	// Single-instance launches (MinCount=MaxCount=1) keep using the existing
	// queue group topic for zero-overhead NATS load balancing.
	if *input.MinCount > 1 || *input.MaxCount > 1 {
		reservationPtr, err := distributeInstances(input, natsConn, accountID)
		if err != nil {
			return reservation, err
		}
		return *reservationPtr, nil
	}

	// Single-instance path: use existing queue group (NATS picks a node with capacity)
	service := handlers_ec2_instance.NewNATSInstanceService(natsConn)

	reservationPtr, err := service.RunInstances(input, accountID)
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
		natsConn, "ec2.DescribeInstanceTypes", &ec2.DescribeInstanceTypesInput{}, 3*time.Second, utils.GlobalAccountID)
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
