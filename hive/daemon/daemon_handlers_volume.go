package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

// handleAttachVolume performs a three-phase hot-plug:
//
//	Phase 1: ebs.mount via NATS   (rolls back with ebs.unmount)
//	Phase 2: QMP blockdev-add     (rolls back Phase 1)
//	Phase 3: QMP device_add       (rolls back Phase 2 + Phase 1)
func (d *Daemon) handleAttachVolume(msg *nats.Msg, command qmp.Command, instance *vm.VM) {
	slog.Info("Attaching volume to instance", "instanceId", command.ID)

	// Validate AttachVolumeData
	if command.AttachVolumeData == nil || command.AttachVolumeData.VolumeID == "" {
		slog.Error("AttachVolume: missing attach volume data")
		respondWithError(msg, awserrors.ErrorInvalidParameterValue)
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
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Validate volume exists and is available
	volCfg, err := d.volumeService.GetVolumeConfig(volumeID)
	if err != nil {
		slog.Error("AttachVolume: failed to get volume config", "volumeId", volumeID, "err", err)
		respondWithError(msg, awserrors.ErrorInvalidVolumeNotFound)
		return
	}

	if volCfg.VolumeMetadata.State != "available" {
		slog.Error("AttachVolume: volume not available", "volumeId", volumeID, "state", volCfg.VolumeMetadata.State)
		respondWithError(msg, awserrors.ErrorVolumeInUse)
		return
	}

	// Check AZ compatibility — volume and instance must be in the same AZ
	if volCfg.VolumeMetadata.AvailabilityZone != "" && d.config.AZ != "" &&
		volCfg.VolumeMetadata.AvailabilityZone != d.config.AZ {
		slog.Error("AttachVolume: volume and instance are in different AZs",
			"volumeId", volumeID, "volumeAZ", volCfg.VolumeMetadata.AvailabilityZone,
			"instanceAZ", d.config.AZ)
		respondWithError(msg, awserrors.ErrorInvalidVolumeZoneMismatch)
		return
	}

	// Determine device name
	if device == "" {
		d.Instances.Mu.Lock()
		device = nextAvailableDevice(instance)
		d.Instances.Mu.Unlock()
		if device == "" {
			slog.Error("AttachVolume: no available device names")
			respondWithError(msg, awserrors.ErrorAttachmentLimitExceeded)
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
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	mountReply, err := d.natsConn.Request(d.ebsTopic("mount"), ebsMountData, 30*time.Second)
	if err != nil {
		slog.Error("AttachVolume: ebs.mount failed", "volumeId", volumeID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	var mountResp config.EBSMountResponse
	if err := json.Unmarshal(mountReply.Data, &mountResp); err != nil {
		slog.Error("AttachVolume: failed to unmarshal mount response", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	if mountResp.Error != "" {
		slog.Error("AttachVolume: mount returned error", "volumeId", volumeID, "err", mountResp.Error)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	nbdURI := mountResp.URI
	if nbdURI == "" {
		slog.Error("AttachVolume: mount response has empty URI", "volumeId", volumeID)
		d.rollbackEBSMount(ebsRequest)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	ebsRequest.NBDURI = nbdURI

	// Parse NBDURI for QMP blockdev-add
	serverType, socketPath, nbdHost, nbdPort, err := utils.ParseNBDURI(nbdURI)
	if err != nil {
		slog.Error("AttachVolume: failed to parse NBDURI", "uri", nbdURI, "err", err)
		// Rollback: unmount
		d.rollbackEBSMount(ebsRequest)
		respondWithError(msg, awserrors.ErrorServerInternal)
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
	iothreadID := fmt.Sprintf("ioth-%s", volumeID)

	// QMP object-add: create iothread for this volume
	iothreadCmd := qmp.QMPCommand{
		Execute: "object-add",
		Arguments: map[string]any{
			"qom-type": "iothread",
			"id":       iothreadID,
		},
	}
	_, err = d.SendQMPCommand(instance.QMPClient, iothreadCmd, instance.ID)
	if err != nil {
		slog.Error("AttachVolume: QMP object-add iothread failed", "volumeId", volumeID, "err", err)
		d.rollbackEBSMount(ebsRequest)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

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
		respondWithError(msg, awserrors.ErrorServerInternal)
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
		"driver":   "virtio-blk-pci",
		"id":       deviceID,
		"drive":    nodeName,
		"iothread": iothreadID,
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
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Discover actual guest device name via QMP query-block
	guestDevice := device // fallback to AWS API name
	deviceMap, qmpErr := queryGuestDeviceMap(d, instance.QMPClient, instance.ID)
	if qmpErr != nil {
		slog.Warn("AttachVolume: failed to query guest device map, using API device name", "volumeId", volumeID, "err", qmpErr)
	} else if gd, ok := deviceMap[deviceID]; ok {
		guestDevice = gd
		slog.Info("AttachVolume: discovered guest device", "volumeId", volumeID, "qemuDevice", deviceID, "guestDevice", guestDevice)
	} else {
		slog.Error("AttachVolume: device not found in QMP device map after successful query, using API device name",
			"volumeId", volumeID, "qemuDevice", deviceID, "deviceMap", deviceMap)
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

	// Update BlockDeviceMappings on the ec2.Instance using actual guest device name
	d.Instances.Mu.Lock()
	if instance.Instance != nil {
		now := time.Now()
		mapping := &ec2.InstanceBlockDeviceMapping{}
		mapping.SetDeviceName(guestDevice)
		mapping.Ebs = &ec2.EbsInstanceBlockDevice{}
		mapping.Ebs.SetVolumeId(volumeID)
		mapping.Ebs.SetAttachTime(now)
		mapping.Ebs.SetDeleteOnTermination(false)
		mapping.Ebs.SetStatus("attached")
		instance.Instance.BlockDeviceMappings = append(instance.Instance.BlockDeviceMappings, mapping)
	}
	d.Instances.Mu.Unlock()

	// Update volume metadata in S3
	if err := d.volumeService.UpdateVolumeState(volumeID, "in-use", command.ID, guestDevice); err != nil {
		slog.Error("AttachVolume: failed to update volume metadata", "volumeId", volumeID, "err", err)
	}

	// Persist state
	if err := d.WriteState(); err != nil {
		slog.Error("AttachVolume: failed to write state", "err", err)
	}

	d.respondWithVolumeAttachment(msg, volumeID, command.ID, guestDevice, "attached")
	slog.Info("Volume attached successfully", "volumeId", volumeID, "instanceId", command.ID, "apiDevice", device, "guestDevice", guestDevice)
}

// handleDetachVolume performs a three-phase hot-unplug (reverse of attach):
//
//	Phase 1: QMP device_del    (remove guest device)
//	Phase 2: QMP blockdev-del  (remove block node)
//	Phase 3: ebs.unmount NATS  (stop NBD server)
func (d *Daemon) handleDetachVolume(msg *nats.Msg, command qmp.Command, instance *vm.VM) {
	slog.Info("Detaching volume from instance", "instanceId", command.ID)

	// Validate DetachVolumeData
	if command.DetachVolumeData == nil || command.DetachVolumeData.VolumeID == "" {
		slog.Error("DetachVolume: missing detach volume data")
		respondWithError(msg, awserrors.ErrorInvalidParameterValue)
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
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
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
		respondWithError(msg, awserrors.ErrorIncorrectState)
		return
	}

	// Reject detaching boot/EFI/CloudInit volumes
	if ebsReq.Boot || ebsReq.EFI || ebsReq.CloudInit {
		slog.Error("DetachVolume: cannot detach boot/EFI/CloudInit volume", "volumeId", volumeID)
		respondWithError(msg, awserrors.ErrorOperationNotPermitted)
		return
	}

	// Optional device cross-check
	if device != "" && ebsReq.DeviceName != "" && device != ebsReq.DeviceName {
		slog.Error("DetachVolume: device mismatch", "requested", device, "actual", ebsReq.DeviceName)
		respondWithError(msg, awserrors.ErrorInvalidParameterValue)
		return
	}

	deviceID := fmt.Sprintf("vdisk-%s", volumeID)
	nodeName := fmt.Sprintf("nbd-%s", volumeID)
	iothreadID := fmt.Sprintf("ioth-%s", volumeID)

	// Phase 1: QMP device_del (remove guest device)
	_, err := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
		Execute:   "device_del",
		Arguments: map[string]any{"id": deviceID},
	}, instance.ID)
	if err != nil {
		if !force {
			slog.Error("DetachVolume: QMP device_del failed", "volumeId", volumeID, "err", err)
			respondWithError(msg, awserrors.ErrorServerInternal)
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
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Phase 2b: QMP object-del (remove iothread, best-effort)
	_, iothreadErr := d.SendQMPCommand(instance.QMPClient, qmp.QMPCommand{
		Execute:   "object-del",
		Arguments: map[string]any{"id": iothreadID},
	}, instance.ID)
	if iothreadErr != nil {
		slog.Warn("DetachVolume: QMP object-del iothread failed (non-fatal)", "volumeId", volumeID, "err", iothreadErr)
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

	d.respondWithVolumeAttachment(msg, volumeID, command.ID, ebsReq.DeviceName, "detaching")
	slog.Info("Volume detached successfully", "volumeId", volumeID, "instanceId", command.ID)
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
	errResp := utils.UnmarshalJsonPayload(modifyVolumeInput, msg.Data)

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
		respondWithError(msg, err.Error())
		return
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("handleEC2ModifyVolume failed to marshal output", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
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
