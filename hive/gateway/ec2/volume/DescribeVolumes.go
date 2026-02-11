package gateway_ec2_volume

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeVolumesInput validates the input parameters
func ValidateDescribeVolumesInput(input *ec2.DescribeVolumesInput) error {
	if input == nil {
		return nil
	}

	// Validate VolumeId format if provided
	if input.VolumeIds != nil {
		for _, volumeId := range input.VolumeIds {
			if volumeId != nil && !strings.HasPrefix(*volumeId, "vol-") {
				return errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
			}
		}
	}

	return nil
}

// DescribeVolumes handles the DescribeVolumes API call
func DescribeVolumes(input *ec2.DescribeVolumesInput, natsConn *nats.Conn) (ec2.DescribeVolumesOutput, error) {
	var output ec2.DescribeVolumesOutput

	// Validate input
	err := ValidateDescribeVolumesInput(input)
	if err != nil {
		return output, err
	}

	// Create NATS service and call handler
	volumeService := handlers_ec2_volume.NewNATSVolumeService(natsConn)
	result, err := volumeService.DescribeVolumes(input)

	if err != nil {
		return output, err
	}

	// Return result
	output = *result
	return output, nil
}
