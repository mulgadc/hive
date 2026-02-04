package gateway_ec2_volume

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/nats-io/nats.go"
)

// DescribeVolumeStatus handles the DescribeVolumeStatus API call
func DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput, natsConn *nats.Conn) (ec2.DescribeVolumeStatusOutput, error) {
	// Validate VolumeId format if provided
	for _, volumeId := range input.VolumeIds {
		if volumeId != nil && !strings.HasPrefix(*volumeId, "vol-") {
			return ec2.DescribeVolumeStatusOutput{}, errors.New(awserrors.ErrorInvalidVolumeIDMalformed)
		}
	}

	volumeService := handlers_ec2_volume.NewNATSVolumeService(natsConn)
	result, err := volumeService.DescribeVolumeStatus(input)
	if err != nil {
		return ec2.DescribeVolumeStatusOutput{}, err
	}

	return *result, nil
}
