package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
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

// CreateSnapshots handles the EC2 CreateSnapshots API call (batch snapshot creation)
func CreateSnapshots(input *ec2.CreateSnapshotsInput, natsConn *nats.Conn) (ec2.CreateSnapshotsOutput, error) {
	var output ec2.CreateSnapshotsOutput

	if err := ValidateCreateSnapshotsInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_snapshot.NewNATSSnapshotService(natsConn)

	// For multi-volume snapshot, we create snapshots for volumes attached to the instance.
	// Current stub: attempt a single snapshot creation and return as batch result.
	var snapshots []*ec2.SnapshotInfo

	createSnapshotInput := &ec2.CreateSnapshotInput{
		Description: input.Description,
	}

	result, err := svc.CreateSnapshot(createSnapshotInput)
	if err != nil {
		// If we can't create a snapshot, return empty result
		output.Snapshots = snapshots
		return output, nil
	}

	snapshotInfo := &ec2.SnapshotInfo{
		SnapshotId:  result.SnapshotId,
		VolumeId:    result.VolumeId,
		VolumeSize:  result.VolumeSize,
		StartTime:   result.StartTime,
		State:       result.State,
		Progress:    result.Progress,
		Encrypted:   result.Encrypted,
		Description: result.Description,
		OwnerId:     aws.String("000000000000"),
		Tags:        []*ec2.Tag{},
	}

	snapshots = append(snapshots, snapshotInfo)
	output.Snapshots = snapshots

	return output, nil
}
