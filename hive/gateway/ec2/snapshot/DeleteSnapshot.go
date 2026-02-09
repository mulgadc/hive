package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/nats-io/nats.go"
)

// ValidateDeleteSnapshotInput validates the input parameters for DeleteSnapshot
func ValidateDeleteSnapshotInput(input *ec2.DeleteSnapshotInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.SnapshotId == nil || *input.SnapshotId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.SnapshotId, "snap-") {
		return errors.New(awserrors.ErrorInvalidSnapshotIDMalformed)
	}

	return nil
}

// DeleteSnapshot handles the EC2 DeleteSnapshot API call
func DeleteSnapshot(input *ec2.DeleteSnapshotInput, natsConn *nats.Conn) (ec2.DeleteSnapshotOutput, error) {
	var output ec2.DeleteSnapshotOutput

	if err := ValidateDeleteSnapshotInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_snapshot.NewNATSSnapshotService(natsConn)
	result, err := svc.DeleteSnapshot(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
