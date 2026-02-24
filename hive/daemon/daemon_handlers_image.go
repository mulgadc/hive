package daemon

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2DescribeImages(msg *nats.Msg) {
	handleNATSRequest(msg, d.imageService.DescribeImages)
}

// handleEC2CreateImage is a stateful handler that extracts instance context
// (root volume ID, source AMI, running state) before delegating to the image service.
func (d *Daemon) handleEC2CreateImage(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject)

	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	input := &ec2.CreateImageInput{}
	if errResp := utils.UnmarshalJsonPayload(input, msg.Data); errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		respondWithError(awserrors.ErrorMissingParameter)
		return
	}

	instanceID := *input.InstanceId

	// Extract all instance context in a single critical section
	d.Instances.Mu.Lock()
	instance, ok := d.Instances.VMS[instanceID]
	var status vm.InstanceState
	var rootVolumeID, sourceImageID string
	if ok {
		status = instance.Status
		if instance.Instance != nil {
			for _, bdm := range instance.Instance.BlockDeviceMappings {
				if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
					rootVolumeID = *bdm.Ebs.VolumeId
					break
				}
			}
			if instance.Instance.ImageId != nil {
				sourceImageID = *instance.Instance.ImageId
			}
		}
	}
	d.Instances.Mu.Unlock()

	if !ok {
		slog.Warn("CreateImage: instance not found", "instanceId", instanceID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if status != vm.StateRunning && status != vm.StateStopped {
		slog.Warn("CreateImage: instance not in valid state", "instanceId", instanceID, "status", status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	if rootVolumeID == "" {
		slog.Error("CreateImage: no root volume found", "instanceId", instanceID)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	params := handlers_ec2_image.CreateImageParams{
		Input:         input,
		RootVolumeID:  rootVolumeID,
		SourceImageID: sourceImageID,
		IsRunning:     status == vm.StateRunning,
	}

	output, err := d.imageService.CreateImageFromInstance(params)
	if err != nil {
		slog.Error("CreateImage: service failed", "instanceId", instanceID, "err", err)
		respondWithError(err.Error())
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("CreateImage: failed to marshal response", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	slog.Info("CreateImage completed", "instanceId", instanceID, "imageId", *output.ImageId)
}
