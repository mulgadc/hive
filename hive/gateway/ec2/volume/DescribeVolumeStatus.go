package gateway_ec2_volume

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeVolumeStatusInput validates the input parameters
func ValidateDescribeVolumeStatusInput(input *ec2.DescribeVolumeStatusInput) error {
	if input == nil {
		return nil
	}

	// Validate VolumeId format if provided
	if input.VolumeIds != nil {
		for _, volumeId := range input.VolumeIds {
			if volumeId != nil && !strings.HasPrefix(*volumeId, "vol-") {
				return errors.New("InvalidVolume.Malformed")
			}
		}
	}

	return nil
}

// DescribeVolumeStatus handles the DescribeVolumeStatus API call
func DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput, natsConn *nats.Conn) (ec2.DescribeVolumeStatusOutput, error) {
	err := ValidateDescribeVolumeStatusInput(input)
	if err != nil {
		return ec2.DescribeVolumeStatusOutput{}, err
	}

	volumeService := handlers_ec2_volume.NewNATSVolumeService(natsConn)
	result, err := volumeService.DescribeVolumeStatus(input)
	if err != nil {
		return ec2.DescribeVolumeStatusOutput{}, err
	}

	return *result, nil
}
