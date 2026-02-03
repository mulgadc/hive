package gateway_ec2_volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_volume "github.com/mulgadc/hive/hive/handlers/ec2/volume"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// ValidateDetachVolumeInput validates the input parameters for DetachVolume
func ValidateDetachVolumeInput(input *ec2.DetachVolumeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.VolumeId == nil || *input.VolumeId == "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	return nil
}

// DetachVolume sends a detach-volume command to the daemon owning the instance
func DetachVolume(input *ec2.DetachVolumeInput, natsConn *nats.Conn) (ec2.VolumeAttachment, error) {
	var output ec2.VolumeAttachment

	if err := ValidateDetachVolumeInput(input); err != nil {
		return output, err
	}

	volumeID := *input.VolumeId

	// Resolve InstanceId if not provided by looking up volume metadata
	instanceID := ""
	if input.InstanceId != nil && *input.InstanceId != "" {
		instanceID = *input.InstanceId
	} else {
		volSvc := handlers_ec2_volume.NewNATSVolumeService(natsConn)
		descOutput, err := volSvc.DescribeVolumes(&ec2.DescribeVolumesInput{
			VolumeIds: []*string{&volumeID},
		})
		if err != nil {
			slog.Error("DetachVolume: failed to describe volume", "volumeId", volumeID, "err", err)
			return output, errors.New(awserrors.ErrorInvalidVolumeNotFound)
		}
		if len(descOutput.Volumes) == 0 {
			return output, errors.New(awserrors.ErrorInvalidVolumeNotFound)
		}
		vol := descOutput.Volumes[0]
		if len(vol.Attachments) == 0 || vol.Attachments[0].InstanceId == nil {
			return output, errors.New(awserrors.ErrorIncorrectState)
		}
		instanceID = *vol.Attachments[0].InstanceId
	}

	device := ""
	if input.Device != nil {
		device = *input.Device
	}

	force := false
	if input.Force != nil {
		force = *input.Force
	}

	command := qmp.Command{
		ID: instanceID,
		Attributes: qmp.Attributes{
			DetachVolume: true,
		},
		DetachVolumeData: &qmp.DetachVolumeData{
			VolumeID: volumeID,
			Device:   device,
			Force:    force,
		},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		slog.Error("DetachVolume: Failed to marshal command", "err", err)
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	subject := fmt.Sprintf("ec2.cmd.%s", instanceID)
	msg, err := natsConn.Request(subject, jsonData, 30*time.Second)
	if err != nil {
		slog.Error("DetachVolume: NATS request failed", "instanceId", instanceID, "volumeId", volumeID, "err", err)
		if errors.Is(err, nats.ErrNoResponders) {
			return output, errors.New(awserrors.ErrorInvalidInstanceIDNotFound)
		}
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	responseError, err := utils.ValidateErrorPayload(msg.Data)
	if err != nil {
		return output, errors.New(*responseError.Code)
	}

	if err := json.Unmarshal(msg.Data, &output); err != nil {
		slog.Error("DetachVolume: Failed to unmarshal response", "err", err)
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DetachVolume completed", "instanceId", instanceID, "volumeId", volumeID)
	return output, nil
}
