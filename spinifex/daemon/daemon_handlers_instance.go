package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/nats-io/nats.go"
)

// handleEC2RunInstances processes incoming EC2 RunInstances requests
func (d *Daemon) handleEC2RunInstances(msg *nats.Msg) {
	slog.Debug("Received message on subject", "subject", msg.Subject)
	slog.Debug("Message data", "data", string(msg.Data))

	// Extract account ID from NATS header
	accountID := utils.AccountIDFromMsg(msg)
	if accountID == "" {
		slog.Error("handleEC2RunInstances: missing account ID in NATS header")
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Initialize runInstancesInput before unmarshaling into it
	runInstancesInput := &ec2.RunInstancesInput{}
	errResp := utils.UnmarshalJsonPayload(runInstancesInput, msg.Data)

	if errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		slog.Error("Request does not match RunInstancesInput")
		return
	}

	slog.Info("Processing RunInstances request for instance type", "instanceType", *runInstancesInput.InstanceType)

	// Check if instance type is supported
	instanceType, exists := d.resourceMgr.instanceTypes[*runInstancesInput.InstanceType]
	if !exists {
		slog.Error("handleEC2RunInstances instance lookup", "err", awserrors.ErrorInvalidInstanceType, "InstanceType", *runInstancesInput.InstanceType)
		respondWithError(msg, awserrors.ErrorInvalidInstanceType)
		return
	}

	// Validate AMI exists before allocating resources
	if runInstancesInput.ImageId == nil || *runInstancesInput.ImageId == "" {
		slog.Error("handleEC2RunInstances missing ImageId")
		respondWithError(msg, awserrors.ErrorMissingParameter)
		return
	}
	if d.imageService == nil {
		slog.Error("handleEC2RunInstances image service not initialized")
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	amiMeta, err := d.imageService.GetAMIConfig(*runInstancesInput.ImageId)
	if err != nil {
		slog.Error("handleEC2RunInstances AMI not found", "imageId", *runInstancesInput.ImageId, "err", err)
		respondWithError(msg, awserrors.ErrorInvalidAMIIDNotFound)
		return
	}
	// Verify the caller can use this AMI: must own it or it must be a system/pre-phase4 AMI.
	// System AMIs have non-account-ID owner aliases (e.g. "self", "spinifex", empty).
	amiOwner := amiMeta.ImageOwnerAlias
	if amiOwner != "" && amiOwner != accountID {
		if utils.IsAccountID(amiOwner) {
			slog.Warn("handleEC2RunInstances AMI not owned by caller", "imageId", *runInstancesInput.ImageId, "amiOwner", amiOwner, "accountID", accountID)
			respondWithError(msg, awserrors.ErrorInvalidAMIIDNotFound)
			return
		}
	}

	// Validate key pair exists (if specified)
	if runInstancesInput.KeyName != nil && *runInstancesInput.KeyName != "" {
		if err := d.keyService.ValidateKeyPairExists(accountID, *runInstancesInput.KeyName); err != nil {
			slog.Error("handleEC2RunInstances key pair not found", "keyName", *runInstancesInput.KeyName, "err", err)
			respondWithError(msg, awserrors.ErrorInvalidKeyPairNotFound)
			return
		}
	}

	// Determine how many instances to launch based on MinCount/MaxCount
	minCount := int(*runInstancesInput.MinCount)
	maxCount := int(*runInstancesInput.MaxCount)

	// Check how many we can actually launch
	allocatableCount := d.resourceMgr.canAllocate(instanceType, maxCount)

	if allocatableCount < minCount {
		// Cannot satisfy MinCount requirement - fail entirely
		slog.Error("handleEC2RunInstances insufficient capacity", "requested", minCount, "available", allocatableCount, "InstanceType", *runInstancesInput.InstanceType)
		respondWithError(msg, awserrors.ErrorInsufficientInstanceCapacity)
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
		respondWithError(msg, awserrors.ErrorInsufficientInstanceCapacity)
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
	var lastRunErr error

	for i := 0; i < launchCount; i++ {
		instance, ec2Instance, err := d.instanceService.RunInstance(runInstancesInput)
		if err != nil {
			slog.Error("handleEC2RunInstances service.RunInstance failed", "index", i, "err", err)
			lastRunErr = err
			// Deallocate this instance's resources
			d.resourceMgr.deallocate(instanceType)
			continue
		}

		// When Terraform sets associate_public_ip_address, it sends the subnet
		// and security groups inside NetworkInterfaces[0] instead of the top-level
		// fields. Extract them so the rest of the handler works uniformly.
		if (runInstancesInput.SubnetId == nil || *runInstancesInput.SubnetId == "") &&
			len(runInstancesInput.NetworkInterfaces) > 0 && runInstancesInput.NetworkInterfaces[0] != nil {
			nic := runInstancesInput.NetworkInterfaces[0]
			if nic.SubnetId != nil && *nic.SubnetId != "" {
				runInstancesInput.SubnetId = nic.SubnetId
			}
			if len(runInstancesInput.SecurityGroupIds) == 0 && len(nic.Groups) > 0 {
				runInstancesInput.SecurityGroupIds = nic.Groups
			}
		}

		// Resolve default subnet when none specified (matches AWS behavior)
		if (runInstancesInput.SubnetId == nil || *runInstancesInput.SubnetId == "") && d.vpcService != nil {
			defaultSubnet, dsErr := d.vpcService.GetDefaultSubnet(accountID)
			if dsErr == nil {
				runInstancesInput.SubnetId = aws.String(defaultSubnet.SubnetId)
				slog.Info("Resolved default subnet for instance", "instanceId", instance.ID, "subnetId", defaultSubnet.SubnetId)
			}
		}

		// Auto-create ENI when SubnetId is provided (matches AWS behavior)
		if runInstancesInput.SubnetId != nil && *runInstancesInput.SubnetId != "" && d.vpcService != nil {
			eniOut, eniErr := d.vpcService.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
				SubnetId:    runInstancesInput.SubnetId,
				Description: aws.String("Primary network interface for " + instance.ID),
			}, accountID)
			if eniErr != nil {
				slog.Error("handleEC2RunInstances auto-create ENI failed", "instanceId", instance.ID, "subnetId", *runInstancesInput.SubnetId, "err", eniErr)
				lastRunErr = eniErr
				d.resourceMgr.deallocate(instanceType)
				continue
			}

			eni := eniOut.NetworkInterface
			instance.ENIId = *eni.NetworkInterfaceId
			instance.ENIMac = *eni.MacAddress

			// Mark ENI as attached to this instance so attachment.instance-id
			// filter works (used by ELBv2 RegisterTargets to resolve target IPs).
			if _, attachErr := d.vpcService.AttachENI(accountID, instance.ENIId, instance.ID, 0); attachErr != nil {
				slog.Error("Failed to attach ENI to instance record — ELBv2 target IP resolution will fail", "eniId", instance.ENIId, "instanceId", instance.ID, "err", attachErr)
			}
			ec2Instance.SetPrivateIpAddress(*eni.PrivateIpAddress)
			ec2Instance.SetSubnetId(*runInstancesInput.SubnetId)
			ec2Instance.SetVpcId(*eni.VpcId)
			ec2Instance.NetworkInterfaces = []*ec2.InstanceNetworkInterface{
				{
					NetworkInterfaceId: eni.NetworkInterfaceId,
					PrivateIpAddress:   eni.PrivateIpAddress,
					MacAddress:         eni.MacAddress,
					SubnetId:           runInstancesInput.SubnetId,
					VpcId:              eni.VpcId,
					Status:             aws.String("in-use"),
					Attachment: &ec2.InstanceNetworkInterfaceAttachment{
						DeviceIndex: aws.Int64(0),
						Status:      aws.String("attached"),
					},
				},
			}

			slog.Info("Auto-created ENI for VPC instance",
				"instanceId", instance.ID,
				"eniId", instance.ENIId,
				"privateIp", *eni.PrivateIpAddress,
				"mac", instance.ENIMac,
			)

			// Auto-assign public IP if subnet has MapPublicIpOnLaunch and external IPAM is available
			if d.externalIPAM != nil {
				subnet, subErr := d.vpcService.GetSubnet(accountID, *runInstancesInput.SubnetId)
				if subErr == nil && subnet.MapPublicIpOnLaunch {
					region := ""
					az := ""
					if d.config != nil {
						region = d.config.Region
						az = d.config.AZ
					}
					publicIP, poolName, allocErr := d.externalIPAM.AllocateIP(region, az, "auto_assign", "", *eni.NetworkInterfaceId, instance.ID)
					if allocErr != nil {
						slog.Warn("Failed to allocate public IP for instance", "instanceId", instance.ID, "err", allocErr)
					} else {
						// Update ENI record with public IP
						if updateErr := d.vpcService.UpdateENIPublicIP(accountID, *eni.NetworkInterfaceId, publicIP, poolName); updateErr != nil {
							slog.Warn("Failed to update ENI with public IP", "eniId", *eni.NetworkInterfaceId, "err", updateErr)
						}
						// Publish vpc.add-nat for dnat_and_snat rule
						portName := "port-" + *eni.NetworkInterfaceId
						d.publishNATEvent("vpc.add-nat", *eni.VpcId, publicIP, *eni.PrivateIpAddress, portName, *eni.MacAddress)
						// Set on ec2Instance response
						ec2Instance.PublicIpAddress = aws.String(publicIP)
						instance.PublicIP = publicIP
						instance.PublicIPPool = poolName
						slog.Info("Auto-assigned public IP",
							"instanceId", instance.ID,
							"publicIp", publicIP,
							"privateIp", *eni.PrivateIpAddress,
							"pool", poolName,
						)
					}
				}
			}
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
		// Propagate the service-layer error if it's a known AWS error code
		errCode := awserrors.ErrorServerInternal
		if lastRunErr != nil {
			if _, ok := awserrors.ErrorLookup[lastRunErr.Error()]; ok {
				errCode = lastRunErr.Error()
			}
		}
		slog.Error("handleEC2RunInstances failed to create minimum instances", "created", len(instances), "minCount", minCount, "err", errCode)
		respondWithError(msg, errCode)
		return
	}

	// Build reservation with all instances
	reservation := ec2.Reservation{}
	reservation.SetReservationId(utils.GenerateResourceID("r"))
	reservation.SetOwnerId(accountID)
	reservation.Instances = allEC2Instances

	// Store reservation reference, account ID, and placement group in all VMs
	for _, instance := range instances {
		instance.Reservation = &reservation
		instance.AccountID = accountID
		if runInstancesInput.Placement != nil && runInstancesInput.Placement.GroupName != nil && *runInstancesInput.Placement.GroupName != "" {
			instance.PlacementGroupName = *runInstancesInput.Placement.GroupName
			instance.PlacementGroupNode = d.node
		}
	}

	// Respond to NATS immediately with reservation (instances are provisioning)
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		slog.Error("handleEC2RunInstances failed to marshal reservation", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
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
	for _, instance := range instances {
		d.vmMgr.Insert(instance)
	}

	if err := d.WriteState(); err != nil {
		slog.Error("handleEC2RunInstances failed to write initial state", "err", err)
	}

	slog.Info("Instances added to state with pending status", "count", len(instances))

	// Subscribe to per-instance NATS topics early so terminate/stop commands
	// can reach this daemon while volumes are being prepared. LaunchInstance
	// will replace these subscriptions when it completes.
	d.mu.Lock()
	for _, instance := range instances {
		sub, subErr := d.natsConn.Subscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), d.handleEC2Events)
		if subErr != nil {
			slog.Error("Failed to early-subscribe to per-instance topic", "instanceId", instance.ID, "err", subErr)
		} else {
			d.natsSubscriptions[instance.ID] = sub
		}
	}
	d.mu.Unlock()

	// Launch all instances (volumes and VMs)
	var successCount int
	for _, instance := range instances {
		// Skip if instance was terminated by a concurrent request
		status := d.vmMgr.Status(instance)
		if status != vm.StatePending && status != vm.StateProvisioning {
			slog.Info("Instance state changed during provisioning, skipping launch",
				"instanceId", instance.ID, "status", string(status))
			continue
		}

		// Pre-compute dev MAC so cloud-init can generate per-interface netplan
		// that suppresses the default route on the dev/hostfwd NIC.
		if d.config.Daemon.DevNetworking && instance.ENIId != "" {
			instance.DevMAC = vm.GenerateDevMAC(instance.ID)
		}

		// Prepare the root volume, cloud-init, EFI drives via NBD (AMI clone to new volume)
		volumeInfos, err := d.instanceService.GenerateVolumes(runInstancesInput, instance)
		if err != nil {
			slog.Error("handleEC2RunInstances GenerateVolumes failed", "instanceId", instance.ID, "err", err)
			d.vmMgr.MarkFailed(instance, "volume_preparation_failed")
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
		err = d.vmMgr.Run(instance)
		if err != nil {
			slog.Error("handleEC2RunInstances vmMgr.Run failed", "instanceId", instance.ID, "err", err)
			d.vmMgr.MarkFailed(instance, "launch_failed")
			continue
		}

		// Discover actual guest device names via QMP query-block
		d.vmMgr.UpdateGuestDeviceNames(instance)

		successCount++
		slog.Info("handleEC2RunInstances launched instance", "instanceId", instance.ID)
	}

	slog.Info("handleEC2RunInstances completed", "requested", launchCount, "created", len(instances), "launched", successCount)
}

func (d *Daemon) handleRebootInstance(msg *nats.Msg, command types.EC2InstanceCommand, instance *vm.VM) {
	slog.Info("Rebooting instance", "id", command.ID)

	if err := d.vmMgr.Reboot(instance.ID); err != nil {
		switch {
		case errors.Is(err, vm.ErrInstanceNotFound):
			respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		case errors.Is(err, vm.ErrInvalidTransition):
			slog.Error("RebootInstance: instance not in running state",
				"instanceId", command.ID, "err", err)
			respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		default:
			slog.Error("RebootInstance: reboot failed", "instanceId", command.ID, "err", err)
			respondWithError(msg, awserrors.ErrorServerInternal)
		}
		return
	}

	slog.Info("Instance rebooted", "instanceId", command.ID)

	if err := msg.Respond([]byte(`{}`)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

func (d *Daemon) handleStartInstance(msg *nats.Msg, command types.EC2InstanceCommand, instance *vm.VM) {
	slog.Info("Starting instance", "id", command.ID)

	// Validate instance is in stopped state
	status := d.vmMgr.Status(instance)

	if status != vm.StateStopped {
		slog.Error("StartInstance: instance not in stopped state", "instanceId", command.ID, "status", status)
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Allocate resources
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if ok {
		if err := d.resourceMgr.allocate(instanceType); err != nil {
			slog.Error("Failed to allocate resources for start command", "id", command.ID, "err", err)
			respondWithError(msg, awserrors.ErrorInsufficientInstanceCapacity)
			return
		}
	}

	// Clear stop attribute before launch so WriteState inside the manager
	// persists the correct attributes. Without this, a daemon restart after
	// a stop→start cycle would see StopInstance=true and skip reconnecting QEMU.
	d.vmMgr.UpdateState(instance.ID, func(v *vm.VM) { v.Attributes = command.Attributes })

	// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
	if err := d.vmMgr.Start(instance.ID); err != nil {
		slog.Error("handleStartInstance: vmMgr.Start failed", "err", err)
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Discover actual guest device names via QMP query-block
	d.vmMgr.UpdateGuestDeviceNames(instance)

	slog.Info("Instance started", "instanceId", instance.ID)

	if err := msg.Respond(fmt.Appendf(nil, `{"status":"running","instanceId":"%s"}`, instance.ID)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

func (d *Daemon) handleStopOrTerminateInstance(msg *nats.Msg, command types.EC2InstanceCommand, instance *vm.VM) {
	isTerminate := command.Attributes.TerminateInstance
	action := "Stopping"
	initialState := vm.StateStopping
	if isTerminate {
		action = "Terminating"
		initialState = vm.StateShuttingDown
	}

	slog.Info(action+" instance", "id", command.ID)

	currentState := d.vmMgr.Status(instance)

	// Idempotent: a concurrent terminate goroutine is already cleaning up.
	if isTerminate && currentState == vm.StateShuttingDown {
		slog.Info("Instance already shutting down, terminate is idempotent", "instanceId", instance.ID)
		if err := msg.Respond([]byte(`{}`)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Validate the transition synchronously before dispatching so the AWS
	// SDK sees IncorrectInstanceState (400) instead of a stale 200. The
	// async Stop/Terminate path re-validates and surfaces vm.ErrInvalidTransition
	// on a racing transition; we map that into the same AWS error below.
	if !vm.IsValidTransition(currentState, initialState) {
		slog.Warn("Instance in incorrect state for "+strings.ToLower(action),
			"instanceId", instance.ID, "currentState", string(currentState))
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Stamp the command attributes onto the VM before dispatch so the persisted
	// state reflects the user-stop / user-terminate intent (e.g. for the
	// recovery path that distinguishes user-stopped from crash-stopped).
	d.vmMgr.UpdateState(instance.ID, func(v *vm.VM) { v.Attributes = command.Attributes })

	// Respond immediately - cleanup completes in background.
	if err := msg.Respond([]byte(`{}`)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	go func(id string) {
		var err error
		if isTerminate {
			err = d.vmMgr.Terminate(id)
		} else {
			err = d.vmMgr.Stop(id)
		}
		if err != nil {
			// Race with another transition: log at the right level so it
			// shows up in dashboards but doesn't trigger paging.
			if errors.Is(err, vm.ErrInvalidTransition) {
				slog.Warn("Lifecycle transition raced; ack already sent",
					"id", id, "action", strings.ToLower(action), "err", err)
				return
			}
			slog.Error("Failed to "+strings.ToLower(action)+" instance", "err", err, "id", id)
		}
	}(instance.ID)
}

// handleEC2DescribeInstances responds with instances running on this node visible to the caller.
func (d *Daemon) handleEC2DescribeInstances(msg *nats.Msg) {
	handleNATSRequest(msg, d.instanceService.DescribeInstances)
}

// handleEC2DescribeInstanceTypes responds with instance types provisionable on this node.
func (d *Daemon) handleEC2DescribeInstanceTypes(msg *nats.Msg) {
	handleNATSRequest(msg, d.instanceService.DescribeInstanceTypes)
}

// startStoppedInstanceRequest is the payload for ec2.start topic
type startStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// handleEC2StartStoppedInstance picks up a stopped instance from shared KV,
// re-launches it on this daemon node, and removes it from shared KV.
func (d *Daemon) handleEC2StartStoppedInstance(msg *nats.Msg) {
	var req startStoppedInstanceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to unmarshal request", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	if req.InstanceID == "" {
		slog.Error("handleEC2StartStoppedInstance: missing instance_id")
		respondWithError(msg, awserrors.ErrorMissingParameter)
		return
	}

	if d.jsManager == nil {
		slog.Error("handleEC2StartStoppedInstance: JetStream not available")
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Load instance from shared KV
	instance, err := d.jsManager.LoadStoppedInstance(req.InstanceID)
	if err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to load stopped instance", "instanceId", req.InstanceID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2StartStoppedInstance: instance not found in shared KV", "instanceId", req.InstanceID)
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2StartStoppedInstance: instance not in stopped state", "instanceId", req.InstanceID, "status", instance.Status)
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Verify the caller owns this instance
	if !checkInstanceOwnership(msg, req.InstanceID, instance.AccountID) {
		return
	}

	// Reset node-local fields that are stale after cross-node migration
	instance.ResetNodeLocalState()

	// Allocate resources
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if !ok {
		slog.Error("handleEC2StartStoppedInstance: instance type not available on this node",
			"instanceId", req.InstanceID, "instanceType", instance.InstanceType)
		respondWithError(msg, awserrors.ErrorInsufficientInstanceCapacity)
		return
	}
	if err := d.resourceMgr.allocate(instanceType); err != nil {
		slog.Error("handleEC2StartStoppedInstance: failed to allocate resources", "instanceId", req.InstanceID, "err", err)
		respondWithError(msg, awserrors.ErrorInsufficientInstanceCapacity)
		return
	}

	// Add instance to local map and clear stop attribute before launch
	instance.Attributes = types.EC2CommandAttributes{StartInstance: true}
	d.vmMgr.Insert(instance)

	// Launch the instance infrastructure (QEMU, QMP, NATS subscriptions)
	err = d.vmMgr.Run(instance)
	if err != nil {
		slog.Error("handleEC2StartStoppedInstance: vmMgr.Run failed", "instanceId", req.InstanceID, "err", err)
		// Rollback: deallocate resources and remove from local map
		if ok {
			d.resourceMgr.deallocate(instanceType)
		}
		d.vmMgr.Delete(instance.ID)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Discover actual guest device names via QMP query-block
	d.vmMgr.UpdateGuestDeviceNames(instance)

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

// terminateStoppedInstanceRequest is the payload for ec2.terminate topic
type terminateStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// handleEC2TerminateStoppedInstance picks up a stopped instance from shared KV,
// deletes its volumes, and removes it from shared KV.
func (d *Daemon) handleEC2TerminateStoppedInstance(msg *nats.Msg) {
	var req terminateStoppedInstanceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to unmarshal request", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	if req.InstanceID == "" {
		slog.Error("handleEC2TerminateStoppedInstance: missing instance_id")
		respondWithError(msg, awserrors.ErrorMissingParameter)
		return
	}

	if d.jsManager == nil {
		slog.Error("handleEC2TerminateStoppedInstance: JetStream not available")
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Load instance from shared KV
	instance, err := d.jsManager.LoadStoppedInstance(req.InstanceID)
	if err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to load stopped instance", "instanceId", req.InstanceID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2TerminateStoppedInstance: instance not found in shared KV", "instanceId", req.InstanceID)
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2TerminateStoppedInstance: instance not in stopped state", "instanceId", req.InstanceID, "status", instance.Status)
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Verify the caller owns this instance
	if !checkInstanceOwnership(msg, req.InstanceID, instance.AccountID) {
		return
	}

	// Delete volumes — no QEMU shutdown or unmount needed (already done during stop)
	instance.EBSRequests.Mu.Lock()
	for _, ebsRequest := range instance.EBSRequests.Requests {
		// Internal volumes (EFI, cloud-init) are always cleaned up via ebs.delete
		if ebsRequest.EFI || ebsRequest.CloudInit {
			ebsDeleteData, err := json.Marshal(types.EBSDeleteRequest{Volume: ebsRequest.Name})
			if err != nil {
				slog.Error("handleEC2TerminateStoppedInstance: failed to marshal ebs.delete request", "name", ebsRequest.Name, "err", err)
				continue
			}
			deleteMsg, err := d.natsConn.Request("ebs.delete", ebsDeleteData, 30*time.Second)
			if err != nil {
				slog.Warn("handleEC2TerminateStoppedInstance: ebs.delete failed for internal volume", "name", ebsRequest.Name, "err", err)
			} else {
				slog.Info("handleEC2TerminateStoppedInstance: ebs.delete sent for internal volume", "name", ebsRequest.Name, "data", string(deleteMsg.Data))
			}
			continue
		}

		// User-visible volumes: respect DeleteOnTermination flag
		if !ebsRequest.DeleteOnTermination {
			slog.Info("handleEC2TerminateStoppedInstance: volume has DeleteOnTermination=false, skipping", "name", ebsRequest.Name)
			continue
		}

		slog.Info("handleEC2TerminateStoppedInstance: deleting volume with DeleteOnTermination=true", "name", ebsRequest.Name)
		_, err := d.volumeService.DeleteVolume(&ec2.DeleteVolumeInput{
			VolumeId: &ebsRequest.Name,
		}, instance.AccountID)
		if err != nil {
			slog.Error("handleEC2TerminateStoppedInstance: failed to delete volume", "name", ebsRequest.Name, "err", err)
		}
	}
	instance.EBSRequests.Mu.Unlock()

	// Release public IP before termination
	if instance.PublicIP != "" && instance.PublicIPPool != "" && d.externalIPAM != nil {
		portName := "port-" + instance.ENIId
		vpcId := ""
		logicalIP := ""
		if instance.Instance != nil {
			if instance.Instance.VpcId != nil {
				vpcId = *instance.Instance.VpcId
			}
			if instance.Instance.PrivateIpAddress != nil {
				logicalIP = *instance.Instance.PrivateIpAddress
			}
		}
		d.publishNATEvent("vpc.delete-nat", vpcId, instance.PublicIP, logicalIP, portName, "")

		if err := d.externalIPAM.ReleaseIP(instance.PublicIPPool, instance.PublicIP); err != nil {
			slog.Warn("handleEC2TerminateStoppedInstance: failed to release public IP", "ip", instance.PublicIP, "pool", instance.PublicIPPool, "err", err)
		} else {
			slog.Info("handleEC2TerminateStoppedInstance: released public IP", "ip", instance.PublicIP, "instanceId", req.InstanceID)
		}
	}

	// Delete ENI if present
	if instance.ENIId != "" && d.vpcService != nil {
		_, eniErr := d.vpcService.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
			NetworkInterfaceId: &instance.ENIId,
		}, instance.AccountID)
		if eniErr != nil {
			slog.Error("handleEC2TerminateStoppedInstance: failed to delete ENI", "eni", instance.ENIId, "err", eniErr)
		} else {
			slog.Info("handleEC2TerminateStoppedInstance: deleted ENI", "eni", instance.ENIId, "instanceId", req.InstanceID)
		}
	}

	// Write to terminated KV bucket FIRST so the instance is visible in DescribeInstances.
	// If this fails, the instance remains in the stopped bucket (safe to retry).
	instance.Status = vm.StateTerminated
	if err := d.jsManager.WriteTerminatedInstance(req.InstanceID, instance); err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to write to terminated KV, aborting", "instanceId", req.InstanceID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	// Now safe to remove from shared stopped KV — instance already exists in terminated bucket.
	// Retry once on failure to avoid duplicate entries in DescribeInstances.
	if err := d.jsManager.DeleteStoppedInstance(req.InstanceID); err != nil {
		slog.Warn("handleEC2TerminateStoppedInstance: first stopped KV delete failed, retrying",
			"instanceId", req.InstanceID, "err", err)
		if retryErr := d.jsManager.DeleteStoppedInstance(req.InstanceID); retryErr != nil {
			slog.Error("handleEC2TerminateStoppedInstance: stopped KV delete failed after retry, instance may appear in both buckets",
				"instanceId", req.InstanceID, "err", retryErr)
		}
	}

	slog.Info("Terminated stopped instance from shared KV", "instanceId", req.InstanceID)

	if err := msg.Respond(fmt.Appendf(nil, `{"status":"terminated","instanceId":"%s"}`, req.InstanceID)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2DescribeStoppedInstances returns stopped instances from shared KV.
func (d *Daemon) handleEC2DescribeStoppedInstances(msg *nats.Msg) {
	handleNATSRequest(msg, d.instanceService.DescribeStoppedInstances)
}

// handleEC2DescribeTerminatedInstances returns terminated instances from the terminated KV bucket.
func (d *Daemon) handleEC2DescribeTerminatedInstances(msg *nats.Msg) {
	handleNATSRequest(msg, d.instanceService.DescribeTerminatedInstances)
}

// handleEC2ModifyInstanceAttribute modifies attributes of a stopped instance in shared KV.
// All supported attributes (InstanceType, UserData) require the instance to be stopped.
func (d *Daemon) handleEC2ModifyInstanceAttribute(msg *nats.Msg) {
	var input ec2.ModifyInstanceAttributeInput
	if err := json.Unmarshal(msg.Data, &input); err != nil {
		slog.Error("handleEC2ModifyInstanceAttribute: failed to unmarshal request", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		slog.Error("handleEC2ModifyInstanceAttribute: missing instance_id")
		respondWithError(msg, awserrors.ErrorMissingParameter)
		return
	}

	instanceID := *input.InstanceId

	// SourceDestCheck is a networking concept that doesn't apply to bare-metal VMs.
	// Accept the call as a no-op so Terraform and the AWS CLI don't error out.
	// Unlike InstanceType/UserData, AWS allows this on running instances, so handle
	// it before the stopped-state gate.
	if input.SourceDestCheck != nil {
		slog.Info("handleEC2ModifyInstanceAttribute: accepting SourceDestCheck (no-op on bare metal)", "instanceId", instanceID)
		if err := msg.Respond([]byte(`{}`)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	if d.jsManager == nil {
		slog.Error("handleEC2ModifyInstanceAttribute: JetStream not available")
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	instance, err := d.jsManager.LoadStoppedInstance(instanceID)
	if err != nil {
		slog.Error("handleEC2ModifyInstanceAttribute: failed to load stopped instance", "instanceId", instanceID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2ModifyInstanceAttribute: instance not found in shared KV", "instanceId", instanceID)
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2ModifyInstanceAttribute: instance not in stopped state", "instanceId", instanceID, "status", instance.Status)
		respondWithError(msg, awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Verify the caller owns this instance
	if !checkInstanceOwnership(msg, instanceID, instance.AccountID) {
		return
	}

	// Apply the requested attribute change
	if input.InstanceType != nil && input.InstanceType.Value != nil {
		newType := *input.InstanceType.Value
		if newType == "" {
			slog.Error("handleEC2ModifyInstanceAttribute: empty instance type value", "instanceId", instanceID)
			respondWithError(msg, awserrors.ErrorInvalidInstanceAttributeValue)
			return
		}
		if instance.Instance == nil {
			slog.Error("handleEC2ModifyInstanceAttribute: instance.Instance is nil, data integrity issue", "instanceId", instanceID)
			respondWithError(msg, awserrors.ErrorServerInternal)
			return
		}
		slog.Info("handleEC2ModifyInstanceAttribute: changing instance type",
			"instanceId", instanceID, "oldType", instance.InstanceType, "newType", newType)

		instance.InstanceType = newType
		instance.Config.InstanceType = newType
		instance.Instance.InstanceType = aws.String(newType)
		// Clear StateReason — resolves capacity-unavailable state from instance-type-missing bug
		instance.Instance.StateReason = nil
	}

	if input.UserData != nil && input.UserData.Value != nil {
		slog.Info("handleEC2ModifyInstanceAttribute: changing user data", "instanceId", instanceID)

		// Value arrives as decoded bytes (JSON unmarshal handles base64 → []byte automatically)
		instance.UserData = string(input.UserData.Value)
		if instance.RunInstancesInput != nil {
			instance.RunInstancesInput.UserData = aws.String(base64.StdEncoding.EncodeToString(input.UserData.Value))
		}
	}

	if err := d.jsManager.WriteStoppedInstance(instanceID, instance); err != nil {
		slog.Error("handleEC2ModifyInstanceAttribute: failed to write modified instance to KV",
			"instanceId", instanceID, "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	slog.Info("handleEC2ModifyInstanceAttribute: completed successfully", "instanceId", instanceID)

	if err := msg.Respond([]byte(`{}`)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2DescribeInstanceAttribute returns a single requested attribute for an instance.
func (d *Daemon) handleEC2DescribeInstanceAttribute(msg *nats.Msg) {
	handleNATSRequest(msg, d.instanceService.DescribeInstanceAttribute)
}

// publishNATEvent sends a NAT lifecycle event (vpc.add-nat or vpc.delete-nat) to NATS.
// For vpc.add-nat, it uses request-reply to ensure the OVN NAT rule is committed
// before returning, preventing ARP propagation races. For vpc.delete-nat, it
// uses fire-and-forget since the caller doesn't need to wait.
func (d *Daemon) publishNATEvent(topic, vpcId, externalIP, logicalIP, portName, mac string) {
	evt := struct {
		VpcId      string `json:"vpc_id"`
		ExternalIP string `json:"external_ip"`
		LogicalIP  string `json:"logical_ip"`
		PortName   string `json:"port_name"`
		MAC        string `json:"mac"`
	}{VpcId: vpcId, ExternalIP: externalIP, LogicalIP: logicalIP, PortName: portName, MAC: mac}

	if topic == "vpc.add-nat" {
		if err := utils.RequestEvent(d.natsConn, topic, evt, 10*time.Second); err != nil {
			slog.Warn("publishNATEvent: failed to add NAT rule — OVN dnat_and_snat rule not created; restart vpcd or re-associate EIP to recover",
				"topic", topic, "externalIP", externalIP, "logicalIP", logicalIP, "err", err)
		}
		return
	}
	utils.PublishEvent(d.natsConn, topic, evt)
}
