package gateway_ec2_volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// ValidateAttachVolumeInput validates the input parameters for AttachVolume
func ValidateAttachVolumeInput(input *ec2.AttachVolumeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.VolumeId == nil || *input.VolumeId == "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	return nil
}

// AttachVolume sends an attach-volume command to the daemon owning the instance
func AttachVolume(input *ec2.AttachVolumeInput, natsConn *nats.Conn) (ec2.VolumeAttachment, error) {
	var output ec2.VolumeAttachment

	if err := ValidateAttachVolumeInput(input); err != nil {
		return output, err
	}

	instanceID := *input.InstanceId
	volumeID := *input.VolumeId

	device := ""
	if input.Device != nil {
		device = *input.Device
	}

	command := qmp.Command{
		ID: instanceID,
		Attributes: qmp.Attributes{
			AttachVolume: true,
		},
		AttachVolumeData: &qmp.AttachVolumeData{
			VolumeID: volumeID,
			Device:   device,
		},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		slog.Error("AttachVolume: Failed to marshal command", "err", err)
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	subject := fmt.Sprintf("ec2.cmd.%s", instanceID)
	msg, err := natsConn.Request(subject, jsonData, 30*time.Second)
	if err != nil {
		slog.Error("AttachVolume: NATS request failed", "instanceId", instanceID, "volumeId", volumeID, "err", err)
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
		slog.Error("AttachVolume: Failed to unmarshal response", "err", err)
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("AttachVolume completed", "instanceId", instanceID, "volumeId", volumeID)
	return output, nil
}
