package gateway_ec2_volume

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/nats-io/nats.go"
)

// ValidateCreateVolumeInput validates the input parameters
func ValidateCreateVolumeInput(input *ec2.CreateVolumeInput) error {
	if input == nil {
		return errors.New("InvalidParameterValue")
	}

	if input.Size == nil || *input.Size <= 0 {
		return errors.New("InvalidParameterValue")
	}

	if input.AvailabilityZone == nil || *input.AvailabilityZone == "" {
		return errors.New("InvalidParameterValue")
	}

	// Only gp3 or empty (defaults to gp3) allowed
	if input.VolumeType != nil && *input.VolumeType != "" && *input.VolumeType != "gp3" {
		return errors.New("InvalidParameterValue")
	}

	return nil
}

// CreateVolume handles the CreateVolume API call
func CreateVolume(input *ec2.CreateVolumeInput, natsConn *nats.Conn) (ec2.Volume, error) {
	var output ec2.Volume

	err := ValidateCreateVolumeInput(input)
	if err != nil {
		return output, err
	}

	volumeService := handlers_ec2_volume.NewNATSVolumeService(natsConn)
	result, err := volumeService.CreateVolume(input)

	if err != nil {
		return output, err
	}

	output = *result
	return output, nil
}
