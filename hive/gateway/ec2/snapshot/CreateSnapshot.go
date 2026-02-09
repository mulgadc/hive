package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/nats-io/nats.go"
)

// ValidateCreateSnapshotInput validates the input parameters for CreateSnapshot
func ValidateCreateSnapshotInput(input *ec2.CreateSnapshotInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.VolumeId == nil || *input.VolumeId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if !strings.HasPrefix(*input.VolumeId, "vol-") {
		return errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
	}

	return nil
}

// CreateSnapshot handles the EC2 CreateSnapshot API call
func CreateSnapshot(input *ec2.CreateSnapshotInput, natsConn *nats.Conn) (ec2.Snapshot, error) {
	var output ec2.Snapshot

	if err := ValidateCreateSnapshotInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_snapshot.NewNATSSnapshotService(natsConn)
	result, err := svc.CreateSnapshot(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
