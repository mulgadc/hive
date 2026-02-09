package gateway_ec2_snapshot

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_snapshot "github.com/mulgadc/hive/hive/handlers/ec2/snapshot"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeSnapshotsInput validates the input parameters for DescribeSnapshots
func ValidateDescribeSnapshotsInput(input *ec2.DescribeSnapshotsInput) error {
	if input == nil {
		return nil
	}

	// Validate snapshot IDs if provided
	for _, id := range input.SnapshotIds {
		if id != nil && *id != "" && !strings.HasPrefix(*id, "snap-") {
			return errors.New(awserrors.ErrorInvalidSnapshotIDMalformed)
		}
	}

	return nil
}

// DescribeSnapshots handles the EC2 DescribeSnapshots API call
func DescribeSnapshots(input *ec2.DescribeSnapshotsInput, natsConn *nats.Conn) (ec2.DescribeSnapshotsOutput, error) {
	var output ec2.DescribeSnapshotsOutput

	if err := ValidateDescribeSnapshotsInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_snapshot.NewNATSSnapshotService(natsConn)
	result, err := svc.DescribeSnapshots(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
