package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2StartInstances(msg *nats.Msg) {

	var ec2StartInstance config.EC2StartInstancesRequest

	if err := json.Unmarshal(msg.Data, &ec2StartInstance); err != nil {
		slog.Error("Error unmarshaling EC2 describe request", "err", err)
		return
	}

	slog.Info("EC2 Start Instance Request", "instanceId", ec2StartInstance.InstanceID)

	var ec2StartInstanceResponse config.EC2StartInstancesResponse

	// Check if the instance is running on this node
	d.Instances.Mu.Lock()
	instance, ok := d.Instances.VMS[ec2StartInstance.InstanceID]
	d.Instances.Mu.Unlock()

	if !ok {
		slog.Error("EC2 Start Request - Instance not found", "instanceId", ec2StartInstanceResponse.InstanceID)
		ec2StartInstanceResponse.InstanceID = ec2StartInstance.InstanceID
		ec2StartInstanceResponse.Error = awserrors.ErrorInvalidInstanceIDNotFound
		ec2StartInstanceResponse.Respond(msg)
		return
	}

	// Check if we have enough resources and allocate them
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if ok {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("EC2 Start Request - Insufficient capacity", "instanceId", instance.ID, "err", err)
			ec2StartInstanceResponse.InstanceID = ec2StartInstance.InstanceID
			ec2StartInstanceResponse.Error = awserrors.ErrorInsufficientInstanceCapacity
			ec2StartInstanceResponse.Respond(msg)
			return
		}
	}

	// Launch the instance (acquires d.Instances.Mu internally via TransitionState)
	err := d.LaunchInstance(instance)

	if err != nil {
		// Deallocate on failure
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
		ec2StartInstanceResponse.Error = err.Error()
	} else {
		ec2StartInstanceResponse.InstanceID = instance.ID
		d.Instances.Mu.Lock()
		ec2StartInstanceResponse.Status = string(instance.Status)
		d.Instances.Mu.Unlock()
	}

	ec2StartInstanceResponse.Respond(msg)

}

// handleEC2Events processes incoming EC2 instance events (start, stop, terminate, attach-volume)
func (d *Daemon) handleEC2Events(msg *nats.Msg) {

	var command qmp.Command
	var resp *qmp.QMPResponse
	var err error

	// Helper to ensure we always respond to NATS
	respondWithError := func(errCode string) {
		msg.Respond(utils.GenerateErrorPayload(errCode))
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

	// AttachVolume performs a three-phase hot-plug:
	//   Phase 1: ebs.mount via NATS   (rolls back with ebs.unmount)
	//   Phase 2: QMP blockdev-add     (rolls back Phase 1)
	//   Phase 3: QMP device_add       (rolls back Phase 2 + Phase 1)
	if command.Attributes.AttachVolume {
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
		return
	}

	// DetachVolume performs a three-phase hot-unplug (reverse of attach):
	//   Phase 1: QMP device_del    (remove guest device)
	//   Phase 2: QMP blockdev-del  (remove block node)
	//   Phase 3: ebs.unmount NATS  (stop NBD server)
	if command.Attributes.DetachVolume {
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
		_, err = d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
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
		time.Sleep(1 * time.Second)

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
		return
	}

	// Start an instance
	if command.Attributes.StartInstance {
		slog.Info("Starting instance", "id", command.ID)

		// Allocate resources
		instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
		if ok {
			if err := d.resourceMgr.allocate(instanceType); err != nil {
				slog.Error("Failed to allocate resources for start command", "id", command.ID, "err", err)
				respondWithError(awserrors.ErrorInsufficientInstanceCapacity)
				return
			}
		}

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

		// Update instance attributes (LaunchInstance already transitions to StateRunning)
		d.Instances.Mu.Lock()
		instance.Attributes = command.Attributes
		d.Instances.Mu.Unlock()

		slog.Info("Instance started", "instanceId", instance.ID)

		msg.Respond(fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID))
		return
	}

	// Stop or terminate an instance
	if command.Attributes.StopInstance || command.Attributes.TerminateInstance {
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
		msg.Respond([]byte(`{}`))

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
				d.Instances.Mu.Unlock()

				if err := d.TransitionState(inst, finalState); err != nil {
					slog.Error("Failed to transition to final state", "instanceId", inst.ID, "err", err)
				}
				slog.Info("Instance "+string(finalState), "id", inst.ID)
			}
		}(instance, command.Attributes)

		return
	}

	// Regular QMP command - must succeed
	resp, err = d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)
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
		msg.Respond(resp.Return)
		return
	}

	if err := json.Unmarshal(resp.Return, target); err != nil {
		slog.Error("Failed to unmarshal QMP response", "cmd", command.QMPCommand.Execute, "err", err)
		msg.Respond(resp.Return)
		return
	}

	// Update attributes and respond
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to write state to disk", "err", err)
	}

	msg.Respond(resp.Return)
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
		msg.Respond(errResp)
		slog.Error("Request does not match RunInstancesInput")
		return
	}

	// Validate inputs
	err := gateway_ec2_instance.ValidateRunInstancesInput(runInstancesInput)

	if err != nil {
		slog.Error("handleEC2RunInstances validation failed", "err", awserrors.ErrorValidationError)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorValidationError)
		msg.Respond(errResp)
		return

	}

	slog.Info("Processing RunInstances request for instance type", "instanceType", *runInstancesInput.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]
	if !exists {
		slog.Error("handleEC2RunInstances instance lookup", "err", awserrors.ErrorInvalidInstanceType, "InstanceType", *runInstancesInput.InstanceType)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidInstanceType)
		msg.Respond(errResp)
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
		msg.Respond(errResp)
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
		msg.Respond(errResp)
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
		msg.Respond(errResp)
		return
	}

	// Build reservation with all instances
	reservation := ec2.Reservation{}
	reservation.SetReservationId(vm.GenerateEC2ReservationID())
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
		msg.Respond(errResp)
		// Deallocate all resources
		for range instances {
			d.resourceMgr.deallocate(instanceType)
		}
		return
	}
	msg.Respond(jsonResponse)

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

// handleEC2CreateKeyPair processes incoming EC2 CreateKeyPair requests
func (d *Daemon) handleEC2CreateKeyPair(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize createKeyPairInput before unmarshaling into it
	createKeyPairInput := &ec2.CreateKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(createKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match CreateKeyPairInput")
		return
	}

	slog.Info("Processing CreateKeyPair request", "keyName", *createKeyPairInput.KeyName)

	// Delegate to key service for business logic (key generation, S3 storage)
	output, err := d.keyService.CreateKeyPair(createKeyPairInput)

	if err != nil {
		slog.Error("handleEC2CreateKeyPair service.CreateKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with CreateKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2CreateKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2CreateKeyPair completed", "keyName", *output.KeyName, "fingerprint", *output.KeyFingerprint)
}

// handleEC2DeleteKeyPair processes incoming EC2 DeleteKeyPair requests
func (d *Daemon) handleEC2DeleteKeyPair(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize deleteKeyPairInput before unmarshaling into it
	deleteKeyPairInput := &ec2.DeleteKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(deleteKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DeleteKeyPairInput")
		return
	}

	// Log which identifier was provided
	if deleteKeyPairInput.KeyPairId != nil {
		slog.Info("Processing DeleteKeyPair request", "keyPairId", *deleteKeyPairInput.KeyPairId)
	} else if deleteKeyPairInput.KeyName != nil {
		slog.Info("Processing DeleteKeyPair request", "keyName", *deleteKeyPairInput.KeyName)
	}

	// Delegate to key service for business logic (S3 deletion)
	output, err := d.keyService.DeleteKeyPair(deleteKeyPairInput)

	if err != nil {
		slog.Error("handleEC2DeleteKeyPair service.DeleteKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DeleteKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DeleteKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DeleteKeyPair completed")
}

// handleEC2DescribeKeyPairs processes incoming EC2 DescribeKeyPairs requests
func (d *Daemon) handleEC2DescribeKeyPairs(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize describeKeyPairsInput before unmarshaling into it
	describeKeyPairsInput := &ec2.DescribeKeyPairsInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeKeyPairsInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeKeyPairsInput")
		return
	}

	slog.Info("Processing DescribeKeyPairs request")

	// Delegate to key service for business logic (S3 listing)
	output, err := d.keyService.DescribeKeyPairs(describeKeyPairsInput)

	if err != nil {
		slog.Error("handleEC2DescribeKeyPairs service.DescribeKeyPairs failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeKeyPairsOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeKeyPairs failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeKeyPairs completed", "count", len(output.KeyPairs))
}

// handleEC2ImportKeyPair processes incoming EC2 ImportKeyPair requests
func (d *Daemon) handleEC2ImportKeyPair(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize importKeyPairInput before unmarshaling into it
	importKeyPairInput := &ec2.ImportKeyPairInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(importKeyPairInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match ImportKeyPairInput")
		return
	}

	// Log which key is being imported (avoid logging the actual key material)
	if importKeyPairInput.KeyName != nil {
		slog.Info("Processing ImportKeyPair request", "keyName", *importKeyPairInput.KeyName)
	}

	// Delegate to key service for business logic (key parsing, S3 storage)
	output, err := d.keyService.ImportKeyPair(importKeyPairInput)

	if err != nil {
		slog.Error("handleEC2ImportKeyPair service.ImportKeyPair failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with ImportKeyPairOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ImportKeyPair failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2ImportKeyPair completed", "keyName", *output.KeyName, "fingerprint", *output.KeyFingerprint)
}

// handleEC2DescribeImages processes incoming EC2 DescribeImages requests
func (d *Daemon) handleEC2DescribeImages(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize describeImagesInput before unmarshaling into it
	describeImagesInput := &ec2.DescribeImagesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeImagesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeImagesInput")
		return
	}

	slog.Info("Processing DescribeImages request")

	// Delegate to image service for business logic (S3 listing)
	output, err := d.imageService.DescribeImages(describeImagesInput)

	if err != nil {
		slog.Error("handleEC2DescribeImages service.DescribeImages failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeImagesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeImages failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeImages completed", "count", len(output.Images))
}

// handleEC2CreateVolume processes incoming EC2 CreateVolume requests
func (d *Daemon) handleEC2CreateVolume(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	createVolumeInput := &ec2.CreateVolumeInput{}
	errResp := utils.UnmarshalJsonPayload(createVolumeInput, msg.Data)
	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match CreateVolumeInput")
		return
	}

	slog.Info("Processing CreateVolume request", "size", aws.Int64Value(createVolumeInput.Size), "az", aws.StringValue(createVolumeInput.AvailabilityZone))

	output, err := d.volumeService.CreateVolume(createVolumeInput)

	if err != nil {
		slog.Error("handleEC2CreateVolume service.CreateVolume failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2CreateVolume failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2CreateVolume completed", "volumeId", aws.StringValue(output.VolumeId))
}

// handleEC2DescribeVolumes processes incoming EC2 DescribeVolumes requests
func (d *Daemon) handleEC2DescribeVolumes(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	// Initialize describeVolumesInput before unmarshaling into it
	describeVolumesInput := &ec2.DescribeVolumesInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(describeVolumesInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DescribeVolumesInput")
		return
	}

	slog.Info("Processing DescribeVolumes request")

	// Delegate to volume service for business logic (S3 listing)
	output, err := d.volumeService.DescribeVolumes(describeVolumesInput)

	if err != nil {
		slog.Error("handleEC2DescribeVolumes service.DescribeVolumes failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	// Respond to NATS with DescribeVolumesOutput
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DescribeVolumes failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DescribeVolumes completed", "count", len(output.Volumes))
}

// handleEC2ModifyVolume processes incoming EC2 ModifyVolume requests
func (d *Daemon) handleEC2ModifyVolume(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	modifyVolumeInput := &ec2.ModifyVolumeInput{}
	var errResp []byte

	errResp = utils.UnmarshalJsonPayload(modifyVolumeInput, msg.Data)

	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match ModifyVolumeInput")
		return
	}

	slog.Info("Processing ModifyVolume request", "volumeId", modifyVolumeInput.VolumeId)

	output, err := d.volumeService.ModifyVolume(modifyVolumeInput)

	if err != nil {
		slog.Error("handleEC2ModifyVolume service.ModifyVolume failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ModifyVolume failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

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

// handleEC2DeleteVolume processes incoming EC2 DeleteVolume requests
func (d *Daemon) handleEC2DeleteVolume(msg *nats.Msg) {
	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	deleteVolumeInput := &ec2.DeleteVolumeInput{}
	errResp := utils.UnmarshalJsonPayload(deleteVolumeInput, msg.Data)
	if errResp != nil {
		msg.Respond(errResp)
		slog.Error("Request does not match DeleteVolumeInput")
		return
	}

	slog.Info("Processing DeleteVolume request", "volumeId", aws.StringValue(deleteVolumeInput.VolumeId))

	output, err := d.volumeService.DeleteVolume(deleteVolumeInput)

	if err != nil {
		slog.Error("handleEC2DeleteVolume service.DeleteVolume failed", "err", err)
		errResp = utils.GenerateErrorPayload(err.Error())
		msg.Respond(errResp)
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2DeleteVolume failed to marshal output", "err", err)
		errResp = utils.GenerateErrorPayload(awserrors.ErrorServerInternal)
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

	slog.Info("handleEC2DeleteVolume completed", "volumeId", aws.StringValue(deleteVolumeInput.VolumeId))
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
		msg.Respond(errResp)
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
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

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
		msg.Respond(errResp)
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
		msg.Respond(errResp)
		return
	}
	msg.Respond(jsonResponse)

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

	msg.Respond(jsonResponse)
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

	msg.Respond(jsonResponse)
	slog.Debug("Node discovery responded", "node", d.node)
}
