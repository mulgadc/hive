package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

// handleNATSRequest is a generic helper for the common unmarshal → service → marshal → respond pattern.
// used for basic requests that don't modify any daemon state, just return the result
func handleNATSRequest[I any, O any](msg *nats.Msg, serviceFn func(*I) (*O, error)) {
	input := new(I)
	if errResp := utils.UnmarshalJsonPayload(input, msg.Data); errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	output, err := serviceFn(input)
	if err != nil {
		if err := msg.Respond(utils.GenerateErrorPayload(err.Error())); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		if err := msg.Respond(utils.GenerateErrorPayload(awserrors.ErrorServerInternal)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2Events processes incoming EC2 instance events (start, stop, terminate, attach-volume)
func (d *Daemon) handleEC2Events(msg *nats.Msg) {
	var command qmp.Command

	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	if err := json.Unmarshal(msg.Data, &command); err != nil {
		slog.Error("Error unmarshaling QMP command", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	d.Instances.Mu.Lock()
	instance, ok := d.Instances.VMS[command.ID]
	d.Instances.Mu.Unlock()

	if !ok {
		slog.Warn("Instance is not running on this node", "id", command.ID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	switch {
	case command.Attributes.AttachVolume:
		d.handleAttachVolume(msg, command, instance, respondWithError)
	case command.Attributes.DetachVolume:
		d.handleDetachVolume(msg, command, instance, respondWithError)
	case command.Attributes.StartInstance:
		d.handleStartInstance(msg, command, instance, respondWithError)
	case command.Attributes.StopInstance, command.Attributes.TerminateInstance:
		d.handleStopOrTerminateInstance(msg, command, instance, respondWithError)
	default:
		d.handleQMPCommand(msg, command, instance, respondWithError)
	}
}

// handleAttachVolume performs a three-phase hot-plug:
//
//	Phase 1: ebs.mount via NATS   (rolls back with ebs.unmount)
//	Phase 2: QMP blockdev-add     (rolls back Phase 1)
//	Phase 3: QMP device_add       (rolls back Phase 2 + Phase 1)
func (d *Daemon) handleAttachVolume(msg *nats.Msg, command qmp.Command, instance *vm.VM, respondWithError func(string)) {
	slog.Info("Attaching volume to instance", "instanceId", command.ID)

	// Validate AttachVolumeData
	if command.AttachVolumeData == nil || command.AttachVolumeData.VolumeID == "" {
		slog.Error("AttachVolume: missing attach volume data")
		respondWithError(awserrors.ErrorInvalidParameterValue)
		return
	}

	volumeID := command.AttachVolumeData.VolumeID
	device := command.AttachVolumeData.Device

	// Validate instance is running
	d.Instances.Mu.Lock()
	status := instance.Status
	d.Instances.Mu.Unlock()

	if status != vm.StateRunning {
		slog.Error("AttachVolume: instance not running", "instanceId", command.ID, "status", status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Validate volume exists and is available
	volCfg, err := d.volumeService.GetVolumeConfig(volumeID)
	if err != nil {
		slog.Error("AttachVolume: failed to get volume config", "volumeId", volumeID, "err", err)
		respondWithError(awserrors.ErrorInvalidVolumeNotFound)
		return
	}

	if volCfg.VolumeMetadata.State != "available" {
		slog.Error("AttachVolume: volume not available", "volumeId", volumeID, "state", volCfg.VolumeMetadata.State)
		respondWithError(awserrors.ErrorVolumeInUse)
		return
	}

	// Determine device name
	if device == "" {
		d.Instances.Mu.Lock()
		device = nextAvailableDevice(instance)
		d.Instances.Mu.Unlock()
		if device == "" {
			slog.Error("AttachVolume: no available device names")
			respondWithError(awserrors.ErrorAttachmentLimitExceeded)
			return
		}
	}

	// Create EBS mount request
	ebsRequest := config.EBSRequest{
		Name:       volumeID,
		DeviceName: device,
	}

	ebsMountData, err := json.Marshal(ebsRequest)
	if err != nil {
		slog.Error("AttachVolume: failed to marshal ebs.mount request", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	mountReply, err := d.natsConn.Request("ebs.mount", ebsMountData, 30*time.Second)
	if err != nil {
		slog.Error("AttachVolume: ebs.mount failed", "volumeId", volumeID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	var mountResp config.EBSMountResponse
	if err := json.Unmarshal(mountReply.Data, &mountResp); err != nil {
		slog.Error("AttachVolume: failed to unmarshal mount response", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if mountResp.Error != "" {
		slog.Error("AttachVolume: mount returned error", "volumeId", volumeID, "err", mountResp.Error)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	nbdURI := mountResp.URI
	if nbdURI == "" {
		slog.Error("AttachVolume: mount response has empty URI", "volumeId", volumeID)
		d.rollbackEBSMount(ebsRequest)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}
	ebsRequest.NBDURI = nbdURI

	// Parse NBDURI for QMP blockdev-add
	serverType, socketPath, nbdHost, nbdPort, err := utils.ParseNBDURI(nbdURI)
	if err != nil {
		slog.Error("AttachVolume: failed to parse NBDURI", "uri", nbdURI, "err", err)
		// Rollback: unmount
		d.rollbackEBSMount(ebsRequest)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Build QMP server argument
	var serverArg map[string]any
	if serverType == "unix" {
		serverArg = map[string]any{"type": "unix", "path": socketPath}
	} else {
		serverArg = map[string]any{"type": "inet", "host": nbdHost, "port": fmt.Sprintf("%d", nbdPort)}
	}

	nodeName := fmt.Sprintf("nbd-%s", volumeID)
	deviceID := fmt.Sprintf("vdisk-%s", volumeID)

	// QMP blockdev-add
	blockdevCmd := qmp.QMPCommand{
		Execute: "blockdev-add",
		Arguments: map[string]any{
			"node-name": nodeName,
			"driver":    "nbd",
			"server":    serverArg,
			"export":    "",
			"read-only": false,
		},
	}

	_, err = d.SendQMPCommand(instance.QMPClient, blockdevCmd, instance.ID)
	if err != nil {
		slog.Error("AttachVolume: QMP blockdev-add failed", "volumeId", volumeID, "err", err)
		d.rollbackEBSMount(ebsRequest)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Determine which hotplug root port to use based on device letter.
	// /dev/sdf -> hotplug1, /dev/sdg -> hotplug2, etc.
	hotplugBus := ""
	if len(device) > 0 {
		letter := device[len(device)-1]
		if letter >= 'f' && letter <= 'p' {
			hotplugBus = fmt.Sprintf("hotplug%d", letter-'f'+1)
		}
	}

	// QMP device_add
	deviceAddArgs := map[string]any{
		"driver": "virtio-blk-pci",
		"id":     deviceID,
		"drive":  nodeName,
	}
	if hotplugBus != "" {
		deviceAddArgs["bus"] = hotplugBus
	}
	deviceAddCmd := qmp.QMPCommand{
		Execute:   "device_add",
		Arguments: deviceAddArgs,
	}

	_, err = d.SendQMPCommand(instance.QMPClient, deviceAddCmd, instance.ID)
	if err != nil {
		slog.Error("AttachVolume: QMP device_add failed, rolling back blockdev", "volumeId", volumeID, "err", err)
		// Rollback: blockdev-del then unmount
		if _, delErr := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
			Execute:   "blockdev-del",
			Arguments: map[string]any{"node-name": nodeName},
		}, instance.ID); delErr != nil {
			slog.Error("AttachVolume: rollback blockdev-del failed, skipping EBS unmount", "volumeId", volumeID, "err", delErr)
		} else {
			d.rollbackEBSMount(ebsRequest)
		}
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Update instance state: replace existing entry for this volume (handles
	// stop/start cycles that keep stale entries) or append a new one.
	instance.EBSRequests.Mu.Lock()
	replaced := false
	for i, req := range instance.EBSRequests.Requests {
		if req.Name == volumeID {
			instance.EBSRequests.Requests[i] = ebsRequest
			replaced = true
			break
		}
	}
	if !replaced {
		instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, ebsRequest)
	}
	instance.EBSRequests.Mu.Unlock()

	// Update BlockDeviceMappings on the ec2.Instance
	d.Instances.Mu.Lock()
	if instance.Instance != nil {
		now := time.Now()
		mapping := &ec2.InstanceBlockDeviceMapping{}
		mapping.SetDeviceName(device)
		mapping.Ebs = &ec2.EbsInstanceBlockDevice{}
		mapping.Ebs.SetVolumeId(volumeID)
		mapping.Ebs.SetAttachTime(now)
		mapping.Ebs.SetDeleteOnTermination(false)
		mapping.Ebs.SetStatus("attached")
		instance.Instance.BlockDeviceMappings = append(instance.Instance.BlockDeviceMappings, mapping)
	}
	d.Instances.Mu.Unlock()

	// Update volume metadata in S3
	if err := d.volumeService.UpdateVolumeState(volumeID, "in-use", command.ID, device); err != nil {
		slog.Error("AttachVolume: failed to update volume metadata", "volumeId", volumeID, "err", err)
	}

	// Persist state
	if err := d.WriteState(); err != nil {
		slog.Error("AttachVolume: failed to write state", "err", err)
	}

	d.respondWithVolumeAttachment(msg, respondWithError, volumeID, command.ID, device, "attached")
	slog.Info("Volume attached successfully", "volumeId", volumeID, "instanceId", command.ID, "device", device)
}

// handleDetachVolume performs a three-phase hot-unplug (reverse of attach):
//
//	Phase 1: QMP device_del    (remove guest device)
//	Phase 2: QMP blockdev-del  (remove block node)
//	Phase 3: ebs.unmount NATS  (stop NBD server)
func (d *Daemon) handleDetachVolume(msg *nats.Msg, command qmp.Command, instance *vm.VM, respondWithError func(string)) {
	slog.Info("Detaching volume from instance", "instanceId", command.ID)

	// Validate DetachVolumeData
	if command.DetachVolumeData == nil || command.DetachVolumeData.VolumeID == "" {
		slog.Error("DetachVolume: missing detach volume data")
		respondWithError(awserrors.ErrorInvalidParameterValue)
		return
	}

	volumeID := command.DetachVolumeData.VolumeID
	device := command.DetachVolumeData.Device
	force := command.DetachVolumeData.Force

	// Validate instance is running
	d.Instances.Mu.Lock()
	status := instance.Status
	d.Instances.Mu.Unlock()

	if status != vm.StateRunning {
		slog.Error("DetachVolume: instance not running", "instanceId", command.ID, "status", status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Find the volume in EBSRequests
	instance.EBSRequests.Mu.Lock()
	var ebsReq config.EBSRequest
	found := false
	for _, req := range instance.EBSRequests.Requests {
		if req.Name == volumeID {
			ebsReq = req
			found = true
			break
		}
	}
	instance.EBSRequests.Mu.Unlock()

	if !found {
		slog.Error("DetachVolume: volume not attached to instance", "volumeId", volumeID, "instanceId", command.ID)
		respondWithError(awserrors.ErrorIncorrectState)
		return
	}

	// Reject detaching boot/EFI/CloudInit volumes
	if ebsReq.Boot || ebsReq.EFI || ebsReq.CloudInit {
		slog.Error("DetachVolume: cannot detach boot/EFI/CloudInit volume", "volumeId", volumeID)
		respondWithError(awserrors.ErrorOperationNotPermitted)
		return
	}

	// Optional device cross-check
	if device != "" && ebsReq.DeviceName != "" && device != ebsReq.DeviceName {
		slog.Error("DetachVolume: device mismatch", "requested", device, "actual", ebsReq.DeviceName)
		respondWithError(awserrors.ErrorInvalidParameterValue)
		return
	}

	deviceID := fmt.Sprintf("vdisk-%s", volumeID)
	nodeName := fmt.Sprintf("nbd-%s", volumeID)

	// Phase 1: QMP device_del (remove guest device)
	_, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
		Execute:   "device_del",
		Arguments: map[string]any{"id": deviceID},
	}, instance.ID)
	if err != nil {
		if !force {
			slog.Error("DetachVolume: QMP device_del failed", "volumeId", volumeID, "err", err)
			respondWithError(awserrors.ErrorServerInternal)
			return
		}
		slog.Warn("DetachVolume: QMP device_del failed (force=true, continuing)", "volumeId", volumeID, "err", err)
	}

	// Brief pause for guest to acknowledge PCI removal
	time.Sleep(d.detachDelay)

	// Phase 2: QMP blockdev-del (remove block node)
	_, blockdevErr := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
		Execute:   "blockdev-del",
		Arguments: map[string]any{"node-name": nodeName},
	}, instance.ID)
	if blockdevErr != nil {
		// Block node still referenced by QEMU; do not clean up state or unmount —
		// tearing down the NBD server would crash the VM, and removing metadata
		// would allow the volume to be double-attached.
		slog.Error("DetachVolume: QMP blockdev-del failed, leaving volume state intact", "volumeId", volumeID, "err", blockdevErr)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Phase 3: ebs.unmount via NATS (best-effort)
	d.rollbackEBSMount(ebsReq)

	// State cleanup: remove volume from EBSRequests (search by name to avoid stale index)
	instance.EBSRequests.Mu.Lock()
	for i, req := range instance.EBSRequests.Requests {
		if req.Name == volumeID {
			instance.EBSRequests.Requests = append(instance.EBSRequests.Requests[:i], instance.EBSRequests.Requests[i+1:]...)
			break
		}
	}
	instance.EBSRequests.Mu.Unlock()

	// Remove from BlockDeviceMappings
	d.Instances.Mu.Lock()
	if instance.Instance != nil {
		filtered := make([]*ec2.InstanceBlockDeviceMapping, 0, len(instance.Instance.BlockDeviceMappings))
		for _, bdm := range instance.Instance.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil && *bdm.Ebs.VolumeId == volumeID {
				continue
			}
			filtered = append(filtered, bdm)
		}
		instance.Instance.BlockDeviceMappings = filtered
	}
	d.Instances.Mu.Unlock()

	// Update volume metadata to "available"
	if err := d.volumeService.UpdateVolumeState(volumeID, "available", "", ""); err != nil {
		slog.Error("DetachVolume: failed to update volume metadata", "volumeId", volumeID, "err", err)
	}

	// Persist state
	if err := d.WriteState(); err != nil {
		slog.Error("DetachVolume: failed to write state", "err", err)
	}

	d.respondWithVolumeAttachment(msg, respondWithError, volumeID, command.ID, ebsReq.DeviceName, "detaching")
	slog.Info("Volume detached successfully", "volumeId", volumeID, "instanceId", command.ID)
}

func (d *Daemon) handleStartInstance(msg *nats.Msg, command qmp.Command, instance *vm.VM, respondWithError func(string)) {
	slog.Info("Starting instance", "id", command.ID)

	// Validate instance is in stopped state
	d.Instances.Mu.Lock()
	status := instance.Status
	d.Instances.Mu.Unlock()

	if status != vm.StateStopped {
		slog.Error("StartInstance: instance not in stopped state", "instanceId", command.ID, "status", status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Allocate resources
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if ok {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("Failed to allocate resources for start command", "id", command.ID, "err", err)
			respondWithError(awserrors.ErrorInsufficientInstanceCapacity)
			return
		}
	}

	// Clear stop attribute before launch so WriteState inside LaunchInstance
	// persists the correct attributes. Without this, a daemon restart after
	// a stop→start cycle would see StopInstance=true and skip reconnecting QEMU.
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

	// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
	err := d.LaunchInstance(instance)

	if err != nil {
		slog.Error("handleEC2RunInstances LaunchInstance failed", "err", err)
		// Free the resource on failure
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Info("Instance started", "instanceId", instance.ID)

	if err := msg.Respond(fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

func (d *Daemon) handleStopOrTerminateInstance(msg *nats.Msg, command qmp.Command, instance *vm.VM, respondWithError func(string)) {
	isTerminate := command.Attributes.TerminateInstance
	action := "Stopping"
	initialState := vm.StateStopping
	finalState := vm.StateStopped
	if isTerminate {
		action = "Terminating"
		initialState = vm.StateShuttingDown
		finalState = vm.StateTerminated
	}

	slog.Info(action+" instance", "id", command.ID)

	// Transition to the initial transitional state
	if err := d.TransitionState(instance, initialState); err != nil {
		slog.Error("Failed to transition to "+string(initialState), "instanceId", instance.ID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Respond immediately - operation will complete in background
	// stopInstance() handles the QMP shutdown command, so we don't send it here
	if err := msg.Respond([]byte(`{}`)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	// Run cleanup in goroutine to not block NATS
	go func(inst *vm.VM, attrs qmp.Attributes) {
		stopErr := d.stopInstance(map[string]*vm.VM{inst.ID: inst}, isTerminate)

		if stopErr != nil {
			slog.Error("Failed to "+strings.ToLower(action)+" instance", "err", stopErr, "id", inst.ID)
			if err := d.TransitionState(inst, vm.StateError); err != nil {
				slog.Error("Failed to transition to error state", "instanceId", inst.ID, "err", err)
			}
		} else {
			d.Instances.Mu.Lock()
			inst.Attributes = attrs
			inst.LastNode = d.node
			d.Instances.Mu.Unlock()

			if err := d.TransitionState(inst, finalState); err != nil {
				slog.Error("Failed to transition to final state", "instanceId", inst.ID, "err", err)
			}
			slog.Info("Instance "+string(finalState), "id", inst.ID)

			// For stop (not terminate): release ownership to shared KV so any
			// daemon can pick up the next StartInstance request.
			if !isTerminate && d.jsManager != nil {

				// Write to shared KV first — if daemon crashes after this but
				// before local cleanup, restoreInstances handles the overlap.
				if err := d.jsManager.WriteStoppedInstance(inst.ID, inst); err != nil {
					slog.Error("Failed to write stopped instance to shared KV, keeping local ownership",
						"instanceId", inst.ID, "err", err)
				} else {
					// Unsubscribe from per-instance NATS topic
					d.mu.Lock()
					if sub, ok := d.natsSubscriptions[inst.ID]; ok {
						if err := sub.Unsubscribe(); err != nil {
							slog.Error("Failed to unsubscribe stopped instance", "instanceId", inst.ID, "err", err)
						}
						delete(d.natsSubscriptions, inst.ID)
					}
					d.mu.Unlock()

					// Remove from local instance map
					d.Instances.Mu.Lock()
					delete(d.Instances.VMS, inst.ID)
					d.Instances.Mu.Unlock()

					// Persist local state without the stopped instance
					if err := d.WriteState(); err != nil {
						slog.Error("Failed to persist state after releasing stopped instance, re-adding to local map for consistency",
							"instanceId", inst.ID, "err", err)
						// Re-add to local map so in-memory state matches on-disk state.
						// On next restart, restoreInstances will retry the migration.
						d.Instances.Mu.Lock()
						d.Instances.VMS[inst.ID] = inst
						d.Instances.Mu.Unlock()
					} else {
						slog.Info("Released stopped instance ownership to shared KV",
							"instanceId", inst.ID, "lastNode", d.node)
					}
				}
			}
		}
	}(instance, command.Attributes)
}

func (d *Daemon) handleQMPCommand(msg *nats.Msg, command qmp.Command, instance *vm.VM, respondWithError func(string)) {
	resp, err := d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)
	if err != nil {
		slog.Error("Failed to send QMP command", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Debug("RAW QMP Response", "resp", string(resp.Return))

	// Unmarshal the response
	target, ok := qmp.CommandResponseTypes[command.QMPCommand.Execute]
	if !ok {
		slog.Warn("Unhandled QMP command", "cmd", command.QMPCommand.Execute)
		if err := msg.Respond(resp.Return); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	if err := json.Unmarshal(resp.Return, target); err != nil {
		slog.Error("Failed to unmarshal QMP response", "cmd", command.QMPCommand.Execute, "err", err)
		if err := msg.Respond(resp.Return); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Update attributes and respond
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to write state to disk", "err", err)
	}

	if err := msg.Respond(resp.Return); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2RunInstances processes incoming EC2 RunInstances requests
func (d *Daemon) handleEC2RunInstances(msg *nats.Msg) {
	slog.Debug("Received message on subject", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	// Initialize runInstancesInput before unmarshaling into it
	runInstancesInput := &ec2.RunInstancesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(runInstancesInput, msg.Data)

	if errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		slog.Error("Request does not match RunInstancesInput")
		return
	}

	// Validate inputs
	err := gateway_ec2_instance.ValidateRunInstancesInput(runInstancesInput)

	if err != nil {
		slog.Error("handleEC2RunInstances validation failed", "err", awserrors.ErrorValidationError)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorValidationError)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return

	}

	slog.Info("Processing RunInstances request for instance type", "instanceType", *runInstancesInput.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]
	if !exists {
		slog.Error("handleEC2RunInstances instance lookup", "err", awserrors.ErrorInvalidInstanceType, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidInstanceType)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Determine how many instances to launch based on MinCount/MaxCount
	minCount := int(*runInstancesInput.MinCount)
	maxCount := int(*runInstancesInput.MaxCount)

	// Check how many we can actually launch
	allocatableCount := d.resourceMgr.canAllocate(instanceType, maxCount)

	if allocatableCount < minCount {
		// Cannot satisfy MinCount requirement - fail entirely
		slog.Error("handleEC2RunInstances insufficient capacity", "requested", minCount, "available", allocatableCount, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Launch up to MaxCount, capped by available capacity
	// Note: canAllocate() already caps at maxCount, so allocatableCount <= maxCount
	launchCount := allocatableCount

	slog.Info("Instance count determined", "min", minCount, "max", maxCount, "launching", launchCount)

	// Allocate resources for all instances upfront
	var allocatedCount int
	for i := 0; i < launchCount; i++ {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("handleEC2RunInstances allocate failed mid-allocation", "allocated", allocatedCount, "err", err)
			break
		}
		allocatedCount++
	}

	// Check if we still meet MinCount after allocation
	if allocatedCount < minCount {
		// Rollback allocations
		for i := 0; i < allocatedCount; i++ {
			d.resourceMgr.deallocate(instanceType)
		}
		slog.Error("handleEC2RunInstances insufficient capacity after allocation", "allocated", allocatedCount, "minCount", minCount)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInsufficientInstanceCapacity)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Update launchCount to what we actually allocated
	launchCount = allocatedCount

	// Delegate to service for business logic (volume creation, cloud-init, etc.)
	instanceTypeName := ""
	if instanceType.InstanceType != nil {
		instanceTypeName = *instanceType.InstanceType
	}
	slog.Info("Launching EC2 instances", "instanceType", instanceTypeName, "count", launchCount)

	// Create all instances
	var instances []*vm.VM
	var allEC2Instances []*ec2.Instance

	for i := 0; i < launchCount; i++ {
		instance, ec2Instance, err := d.instanceService.RunInstance(runInstancesInput)
		if err != nil {
			slog.Error("handleEC2RunInstances service.RunInstance failed", "index", i, "err", err)
			// Deallocate this instance's resources
			d.resourceMgr.deallocate(instanceType)
			continue
		}
		instances = append(instances, instance)
		allEC2Instances = append(allEC2Instances, ec2Instance)
	}

	// Check if we still have enough instances after creation errors
	if len(instances) < minCount {
		// Rollback: deallocate resources for successfully created instances
		// (failed instances already deallocated their resources above)
		for range instances {
			d.resourceMgr.deallocate(instanceType)
		}
		slog.Error("handleEC2RunInstances failed to create minimum instances", "created", len(instances), "minCount", minCount)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Build reservation with all instances
	reservation := ec2.Reservation{}
	reservation.SetReservationId(utils.GenerateResourceID("r"))
	reservation.SetOwnerId("123456789012") // TODO: Use actual owner ID from config
	reservation.Instances = allEC2Instances

	// Store reservation reference in all VMs
	for _, instance := range instances {
		instance.Reservation = &reservation
	}

	// Respond to NATS immediately with reservation (instances are provisioning)
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		slog.Error("handleEC2RunInstances failed to marshal reservation", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		// Deallocate all resources
		for range instances {
			d.resourceMgr.deallocate(instanceType)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	// Add all instances to state immediately so DescribeInstances can find them
	// while volumes are being prepared and VMs are launching
	d.Instances.Mu.Lock()
	for _, instance := range instances {
		d.Instances.VMS[instance.ID] = instance
	}
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("handleEC2RunInstances failed to write initial state", "err", err)
	}

	slog.Info("Instances added to state with pending status", "count", len(instances))

	// Launch all instances (volumes and VMs)
	var successCount int
	for _, instance := range instances {
		// Prepare the root volume, cloud-init, EFI drives via NBD (AMI clone to new volume)
		volumeInfos, err := d.instanceService.GenerateVolumes(runInstancesInput, instance)
		if err != nil {
			slog.Error("handleEC2RunInstances GenerateVolumes failed", "instanceId", instance.ID, "err", err)
			d.resourceMgr.deallocate(instanceType)
			d.markInstanceFailed(instance, "volume_preparation_failed")
			continue
		}

		// Populate BlockDeviceMappings on the ec2.Instance
		instance.Instance.BlockDeviceMappings = make([]*ec2.InstanceBlockDeviceMapping, 0, len(volumeInfos))
		for _, vi := range volumeInfos {
			mapping := &ec2.InstanceBlockDeviceMapping{}
			mapping.SetDeviceName(vi.DeviceName)
			mapping.Ebs = &ec2.EbsInstanceBlockDevice{}
			mapping.Ebs.SetVolumeId(vi.VolumeId)
			mapping.Ebs.SetAttachTime(vi.AttachTime)
			mapping.Ebs.SetDeleteOnTermination(vi.DeleteOnTermination)
			mapping.Ebs.SetStatus("attached")
			instance.Instance.BlockDeviceMappings = append(instance.Instance.BlockDeviceMappings, mapping)
		}

		// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
		err = d.LaunchInstance(instance)
		if err != nil {
			slog.Error("handleEC2RunInstances LaunchInstance failed", "instanceId", instance.ID, "err", err)
			d.resourceMgr.deallocate(instanceType)
			d.markInstanceFailed(instance, "launch_failed")
			continue
		}

		successCount++
		slog.Info("handleEC2RunInstances launched instance", "instanceId", instance.ID)
	}

	slog.Info("handleEC2RunInstances completed", "requested", launchCount, "created", len(instances), "launched", successCount)
}

func (d *Daemon) handleEC2CreateKeyPair(msg *nats.Msg) {
	handleNATSRequest(msg, d.keyService.CreateKeyPair)
}

func (d *Daemon) handleEC2DeleteKeyPair(msg *nats.Msg) {
	handleNATSRequest(msg, d.keyService.DeleteKeyPair)
}

func (d *Daemon) handleEC2DescribeKeyPairs(msg *nats.Msg) {
	handleNATSRequest(msg, d.keyService.DescribeKeyPairs)
}

func (d *Daemon) handleEC2ImportKeyPair(msg *nats.Msg) {
	handleNATSRequest(msg, d.keyService.ImportKeyPair)
}

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

func (d *Daemon) handleEC2CreateVolume(msg *nats.Msg) {
	handleNATSRequest(msg, d.volumeService.CreateVolume)
}

func (d *Daemon) handleEC2DescribeVolumes(msg *nats.Msg) {
	handleNATSRequest(msg, d.volumeService.DescribeVolumes)
}

func (d *Daemon) handleEC2DescribeVolumeStatus(msg *nats.Msg) {
	handleNATSRequest(msg, d.volumeService.DescribeVolumeStatus)
}

// handleEC2ModifyVolume processes incoming EC2 ModifyVolume requests
func (d *Daemon) handleEC2ModifyVolume(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	modifyVolumeInput := &ec2.ModifyVolumeInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(modifyVolumeInput, msg.Data)

	if errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		slog.Error("Request does not match ModifyVolumeInput")
		return
	}

	slog.Info("Processing ModifyVolume request", "volumeId", modifyVolumeInput.VolumeId)

	output, err := d.volumeService.ModifyVolume(modifyVolumeInput)

	if err != nil {
		slog.Error("handleEC2ModifyVolume service.ModifyVolume failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ModifyVolume failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	// Notify viperblockd to reload state after volume modification (e.g. resize)
	if modifyVolumeInput.VolumeId != nil {
		syncData, err := json.Marshal(config.EBSSyncRequest{Volume: *modifyVolumeInput.VolumeId})
		if err != nil {
			slog.Error("failed to marshal ebs.sync request", "volumeId", *modifyVolumeInput.VolumeId, "err", err)
		} else {
			_, syncErr := d.natsConn.Request("ebs.sync", syncData, 5*time.Second)
			if syncErr != nil {
				slog.Warn("ebs.sync notification failed (volume may not be mounted)",
					"volumeId", *modifyVolumeInput.VolumeId, "err", syncErr)
			}
		}
	}

	slog.Info("handleEC2ModifyVolume completed", "volumeId", modifyVolumeInput.VolumeId)
}

func (d *Daemon) handleEC2DeleteVolume(msg *nats.Msg) {
	handleNATSRequest(msg, d.volumeService.DeleteVolume)
}

func (d *Daemon) handleEC2CreateSnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.CreateSnapshot)
}

func (d *Daemon) handleEC2DescribeSnapshots(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.DescribeSnapshots)
}

func (d *Daemon) handleEC2DeleteSnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.DeleteSnapshot)
}

func (d *Daemon) handleEC2CopySnapshot(msg *nats.Msg) {
	handleNATSRequest(msg, d.snapshotService.CopySnapshot)
}

func (d *Daemon) handleEC2CreateTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.CreateTags)
}

func (d *Daemon) handleEC2DeleteTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DeleteTags)
}

func (d *Daemon) handleEC2DescribeTags(msg *nats.Msg) {
	handleNATSRequest(msg, d.tagsService.DescribeTags)
}

func (d *Daemon) handleEC2CreateEgressOnlyInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.CreateEgressOnlyInternetGateway)
}

func (d *Daemon) handleEC2DeleteEgressOnlyInternetGateway(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.DeleteEgressOnlyInternetGateway)
}

func (d *Daemon) handleEC2DescribeEgressOnlyInternetGateways(msg *nats.Msg) {
	handleNATSRequest(msg, d.eigwService.DescribeEgressOnlyInternetGateways)
}

// handleEC2DescribeInstanceTypes processes incoming EC2 DescribeInstanceTypes requests
// This handler responds with instance types that can currently be provisioned on this node
// based on available resources (CPU and memory not already allocated to running instances)
func (d *Daemon) handleEC2DescribeInstanceTypes(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject)

	// Initialize input
	describeInput := &ec2.DescribeInstanceTypesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeInput, msg.Data)
	if errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		slog.Error("Request does not match DescribeInstanceTypesInput")
		return
	}

	slog.Info("Processing DescribeInstanceTypes request from this node")

	// Check if "capacity" filter is set to "true"
	showCapacity := false
	for _, f := range describeInput.Filters {
		if f.Name != nil && *f.Name == "capacity" {
			for _, v := range f.Values {
				if v != nil && *v == "true" {
					showCapacity = true
					break
				}
			}
		}
	}

	// Get instance types based on capacity and the showCapacity flag
	filteredTypes := d.resourceMgr.GetAvailableInstanceTypeInfos(showCapacity)

	// Create the response
	output := &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: filteredTypes,
	}

	// Respond to NATS
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeInstanceTypes failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	slog.Info("handleEC2DescribeInstanceTypes completed", "count", len(filteredTypes))
}

// handleEC2DescribeInstances processes incoming EC2 DescribeInstances requests
// This handler responds with all instances running on this node
func (d *Daemon) handleEC2DescribeInstances(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize describeInstancesInput before unmarshaling into it
	describeInstancesInput := &ec2.DescribeInstancesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeInstancesInput, msg.Data)

	if errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		slog.Error("Request does not match DescribeInstancesInput")
		return
	}

	slog.Info("Processing DescribeInstances request from this node")

	d.Instances.Mu.Lock()
	defer d.Instances.Mu.Unlock()

	// Filter instances if specific instance IDs were requested
	instanceIDFilter := make(map[string]bool)
	if len(describeInstancesInput.InstanceIds) > 0 {
		for _, id := range describeInstancesInput.InstanceIds {
			if id != nil {
				instanceIDFilter[*id] = true
			}
		}
	}

	// Group instances by reservation ID (AWS returns instances grouped by reservation)
	reservationMap := make(map[string]*ec2.Reservation)

	// Iterate through all instances on this node
	for _, instance := range d.Instances.VMS {
		// Skip if filtering by instance IDs and this instance is not in the filter
		if len(instanceIDFilter) > 0 && !instanceIDFilter[instance.ID] {
			continue
		}

		// Use stored reservation metadata if available
		if instance.Reservation != nil && instance.Instance != nil {
			resID := ""
			if instance.Reservation.ReservationId != nil {
				resID = *instance.Reservation.ReservationId
			}

			// Create reservation entry if it doesn't exist
			if _, exists := reservationMap[resID]; !exists {
				reservation := &ec2.Reservation{}
				reservation.SetReservationId(resID)
				if instance.Reservation.OwnerId != nil {
					reservation.SetOwnerId(*instance.Reservation.OwnerId)
				}
				reservation.Instances = []*ec2.Instance{}
				reservationMap[resID] = reservation
			}

			// Update the instance state to current state
			instanceCopy := *instance.Instance
			instanceCopy.State = &ec2.InstanceState{}

			// Map internal status to EC2 state codes using the centralized mapping
			if info, ok := vm.EC2StateCodes[instance.Status]; ok {
				instanceCopy.State.SetCode(info.Code)
				instanceCopy.State.SetName(info.Name)
			} else {
				slog.Warn("Instance has unmapped status, reporting as pending",
					"instanceId", instance.ID, "status", string(instance.Status))
				instanceCopy.State.SetCode(0)
				instanceCopy.State.SetName("pending")
			}

			// Add instance to its reservation
			reservationMap[resID].Instances = append(reservationMap[resID].Instances, &instanceCopy)
		}
	}

	// Convert map to slice
	reservations := make([]*ec2.Reservation, 0, len(reservationMap))
	for _, reservation := range reservationMap {
		reservations = append(reservations, reservation)
	}

	// Create the response
	output := &ec2.DescribeInstancesOutput{
		Reservations: reservations,
	}

	// Respond to NATS with DescribeInstancesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeInstances failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	slog.Info("handleEC2DescribeInstances completed", "count", len(reservations))
}

// handleHealthCheck processes NATS health check requests
func (d *Daemon) handleHealthCheck(msg *nats.Msg) {
	configHash, err := d.computeConfigHash()
	if err != nil {
		slog.Error("Failed to compute config hash for health check", "error", err)
		configHash = "error"
	}

	response := config.NodeHealthResponse{
		Node:       d.node,
		Status:     "running",
		ConfigHash: configHash,
		Epoch:      d.clusterConfig.Epoch,
		Uptime:     int64(time.Since(d.startTime).Seconds()),
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleHealthCheck failed to marshal response", "err", err)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
	slog.Debug("Health check responded", "node", d.node, "epoch", d.clusterConfig.Epoch)
}

// NodeDiscoverResponse is the response for node discovery requests
type NodeDiscoverResponse struct {
	Node string `json:"node"`
}

// handleNodeDiscover responds to node discovery requests with this node's ID
// Used by the gateway to dynamically discover active hive nodes in the cluster
func (d *Daemon) handleNodeDiscover(msg *nats.Msg) {
	response := NodeDiscoverResponse{
		Node: d.node,
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleNodeDiscover failed to marshal response", "err", err)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
	slog.Debug("Node discovery responded", "node", d.node)
}

// Account settings handlers

func (d *Daemon) handleEC2EnableEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.EnableEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2DisableEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.DisableEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2GetEbsEncryptionByDefault(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.GetEbsEncryptionByDefault)
}

func (d *Daemon) handleEC2GetSerialConsoleAccessStatus(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.GetSerialConsoleAccessStatus)
}

func (d *Daemon) handleEC2EnableSerialConsoleAccess(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.EnableSerialConsoleAccess)
}

func (d *Daemon) handleEC2DisableSerialConsoleAccess(msg *nats.Msg) {
	handleNATSRequest(msg, d.accountService.DisableSerialConsoleAccess)
}

// startStoppedInstanceRequest is the payload for ec2.start topic
type startStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// handleEC2StartStoppedInstance picks up a stopped instance from shared KV,
// re-launches it on this daemon node, and removes it from shared KV.
func (d *Daemon) handleEC2StartStoppedInstance(msg *nats.Msg) {
	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	var req startStoppedInstanceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to unmarshal request", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if req.InstanceID == "" {
		slog.Error("handleEC2StartStoppedInstance: missing instance_id")
		respondWithError(awserrors.ErrorMissingParameter)
		return
	}

	if d.jsManager == nil {
		slog.Error("handleEC2StartStoppedInstance: JetStream not available")
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Load instance from shared KV
	instance, err := d.jsManager.LoadStoppedInstance(req.InstanceID)
	if err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to load stopped instance", "instanceId", req.InstanceID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2StartStoppedInstance: instance not found in shared KV", "instanceId", req.InstanceID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2StartStoppedInstance: instance not in stopped state", "instanceId", req.InstanceID, "status", instance.Status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Reset node-local fields that are stale after cross-node migration
	instance.ResetNodeLocalState()

	// Allocate resources
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if !ok {
		slog.Error("handleEC2StartStoppedInstance: unknown instance type", "instanceId", req.InstanceID, "instanceType", instance.InstanceType)
		respondWithError(awserrors.ErrorInvalidInstanceType)
		return
	}
	if err := d.resourceMgr.allocate(instanceType); err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to allocate resources", "instanceId", req.InstanceID, "err", err)
		respondWithError(awserrors.ErrorInsufficientInstanceCapacity)
		return
	}

	// Add instance to local map and clear stop attribute before launch
	d.Instances.Mu.Lock()
	d.Instances.VMS[instance.ID] = instance
	instance.Attributes = qmp.Attributes{StartInstance: true}
	d.Instances.Mu.Unlock()

	// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
	err = d.LaunchInstance(instance)
	if err != nil {
		slog.Error("handleEC2StartStoppedInstance: LaunchInstance failed", "instanceId", req.InstanceID, "err", err)
		// Rollback: deallocate resources and remove from local map
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
		d.Instances.Mu.Lock()
		delete(d.Instances.VMS, instance.ID)
		d.Instances.Mu.Unlock()
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Remove from shared KV now that it's running locally.
	// Retry once on failure — a stale KV entry risks duplicate starts.
	if err := d.jsManager.DeleteStoppedInstance(req.InstanceID); err != nil {
		slog.Warn("handleEC2StartStoppedInstance: first KV delete failed, retrying",
			"instanceId", req.InstanceID, "err", err)
		if retryErr := d.jsManager.DeleteStoppedInstance(req.InstanceID); retryErr != nil {
			slog.Error("handleEC2StartStoppedInstance: KV delete failed after retry, instance is running locally but stale entry remains in shared KV",
				"instanceId", req.InstanceID, "err", retryErr)
		}
	}

	slog.Info("Started stopped instance from shared KV", "instanceId", instance.ID, "node", d.node)

	if err := msg.Respond(fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2DescribeStoppedInstances returns stopped instances from shared KV.
func (d *Daemon) handleEC2DescribeStoppedInstances(msg *nats.Msg) {
	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	if d.jsManager == nil {
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Parse optional filters from the request
	describeInput := &ec2.DescribeInstancesInput{}
	if len(msg.Data) > 0 {
		if errResp := utils.UnmarshalJsonPayload(describeInput, msg.Data); errResp != nil {
			if err := msg.Respond(errResp); err != nil {
				slog.Error("Failed to respond to NATS request", "err", err)
			}
			return
		}
	}

	// Build instance ID filter
	instanceIDFilter := make(map[string]bool)
	if len(describeInput.InstanceIds) > 0 {
		for _, id := range describeInput.InstanceIds {
			if id != nil {
				instanceIDFilter[*id] = true
			}
		}
	}

	instances, err := d.jsManager.ListStoppedInstances()
	if err != nil {
		slog.Error("handleEC2DescribeStoppedInstances: failed to list stopped instances", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Group instances by reservation ID
	reservationMap := make(map[string]*ec2.Reservation)

	for _, instance := range instances {
		// Apply instance ID filter
		if len(instanceIDFilter) > 0 && !instanceIDFilter[instance.ID] {
			continue
		}

		if instance.Reservation != nil && instance.Instance != nil {
			resID := ""
			if instance.Reservation.ReservationId != nil {
				resID = *instance.Reservation.ReservationId
			}

			if _, exists := reservationMap[resID]; !exists {
				reservation := &ec2.Reservation{}
				reservation.SetReservationId(resID)
				if instance.Reservation.OwnerId != nil {
					reservation.SetOwnerId(*instance.Reservation.OwnerId)
				}
				reservation.Instances = []*ec2.Instance{}
				reservationMap[resID] = reservation
			}

			// Update the instance state
			instanceCopy := *instance.Instance
			instanceCopy.State = &ec2.InstanceState{}
			if info, ok := vm.EC2StateCodes[instance.Status]; ok {
				instanceCopy.State.SetCode(info.Code)
				instanceCopy.State.SetName(info.Name)
			} else {
				instanceCopy.State.SetCode(80)
				instanceCopy.State.SetName("stopped")
			}

			reservationMap[resID].Instances = append(reservationMap[resID].Instances, &instanceCopy)
		}
	}

	reservations := make([]*ec2.Reservation, 0, len(reservationMap))
	for _, reservation := range reservationMap {
		reservations = append(reservations, reservation)
	}

	output := &ec2.DescribeInstancesOutput{
		Reservations: reservations,
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeStoppedInstances: failed to marshal output", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	slog.Info("handleEC2DescribeStoppedInstances completed", "count", len(reservations))
}
