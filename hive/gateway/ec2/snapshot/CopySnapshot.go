package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/nats-io/nats.go"
)

// ValidateCopySnapshotInput validates the input parameters for CopySnapshot
func ValidateCopySnapshotInput(input *ec2.CopySnapshotInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.SourceSnapshotId == nil || *input.SourceSnapshotId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.SourceSnapshotId, "snap-") {
		return errors.New(awserrors.ErrorInvalidSnapshotIDMalformed)
	}

	if input.SourceRegion == nil || *input.SourceRegion == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	return nil
}

// CopySnapshot handles the EC2 CopySnapshot API call
func CopySnapshot(input *ec2.CopySnapshotInput, natsConn *nats.Conn) (ec2.CopySnapshotOutput, error) {
	var output ec2.CopySnapshotOutput

	if err := ValidateCopySnapshotInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_snapshot.NewNATSSnapshotService(natsConn)
	result, err := svc.CopySnapshot(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
