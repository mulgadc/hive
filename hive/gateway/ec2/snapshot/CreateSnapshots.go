package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/nats-io/nats.go"
)

// ValidateCreateSnapshotsInput validates the input parameters for CreateSnapshots
func ValidateCreateSnapshotsInput(input *ec2.CreateSnapshotsInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.InstanceSpecification == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.InstanceSpecification.InstanceId == nil || *input.InstanceSpecification.InstanceId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.InstanceSpecification.InstanceId, "i-") {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}

	return nil
}

// CreateSnapshots handles the EC2 CreateSnapshots API call (batch snapshot creation).
// Stub: requires instance-volume attachment tracking to discover which volumes to snapshot.
// Currently returns an empty snapshot list after validation.
func CreateSnapshots(input *ec2.CreateSnapshotsInput, natsConn *nats.Conn) (ec2.CreateSnapshotsOutput, error) {
	var output ec2.CreateSnapshotsOutput

	if err := ValidateCreateSnapshotsInput(input); err != nil {
		return output, err
	}

	// TODO: look up volumes attached to the instance and create snapshots for each
	return output, nil
}
