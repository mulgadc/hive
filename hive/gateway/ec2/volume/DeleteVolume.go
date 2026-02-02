package gateway_ec2_volume

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/nats-io/nats.go"
)

// ValidateDeleteVolumeInput validates the input parameters
func ValidateDeleteVolumeInput(input *ec2.DeleteVolumeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.VolumeId == nil || len(*input.VolumeId) <= len("vol-") || !strings.HasPrefix(*input.VolumeId, "vol-") {
		return errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
	}

	return nil
}

// DeleteVolume handles the DeleteVolume API call
func DeleteVolume(input *ec2.DeleteVolumeInput, natsConn *nats.Conn) (ec2.DeleteVolumeOutput, error) {
	var output ec2.DeleteVolumeOutput

	err := ValidateDeleteVolumeInput(input)
	if err != nil {
		return output, err
	}

	volumeService := handlers_ec2_volume.NewNATSVolumeService(natsConn)
	result, err := volumeService.DeleteVolume(input)

	if err != nil {
		return output, err
	}

	output = *result
	return output, nil
}
