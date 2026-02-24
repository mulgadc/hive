package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

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

	// Validate AMI exists before allocating resources
	if runInstancesInput.ImageId != nil {
		_, err := d.imageService.GetAMIConfig(*runInstancesInput.ImageId)
		if err != nil {
			slog.Error("handleEC2RunInstances AMI not found", "imageId", *runInstancesInput.ImageId, "err", err)
			errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidAMIIDNotFound)
			if err := msg.Respond(errResp); err != nil {
				slog.Error("Failed to respond to NATS request", "err", err)
			}
			return
		}
	}

	// Validate key pair exists (if specified)
	if runInstancesInput.KeyName != nil && *runInstancesInput.KeyName != "" {
		if err := d.keyService.ValidateKeyPairExists(*runInstancesInput.KeyName); err != nil {
			slog.Error("handleEC2RunInstances key pair not found", "keyName", *runInstancesInput.KeyName, "err", err)
			errResp = utils.GenerateErrorPayload(awserrors.ErrorInvalidKeyPairNotFound)
			if err := msg.Respond(errResp); err != nil {
				slog.Error("Failed to respond to NATS request", "err", err)
			}
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

		// Auto-create ENI when SubnetId is provided (matches AWS behavior)
		if runInstancesInput.SubnetId != nil && *runInstancesInput.SubnetId != "" && d.vpcService != nil {
			eniOut, eniErr := d.vpcService.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
				SubnetId:    runInstancesInput.SubnetId,
				Description: aws.String("Primary network interface for " + instance.ID),
			})
			if eniErr != nil {
				slog.Error("handleEC2RunInstances auto-create ENI failed", "instanceId", instance.ID, "subnetId", *runInstancesInput.SubnetId, "err", eniErr)
				lastRunErr = eniErr
				d.resourceMgr.deallocate(instanceType)
				continue
			}

			eni := eniOut.NetworkInterface
			instance.ENIId = *eni.NetworkInterfaceId
			instance.ENIMac = *eni.MacAddress
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
		errResp = utils.GenerateErrorPayload(errCode)
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

	// Check state validity before attempting transition — return the correct
	// AWS error code when the instance is already stopped/terminated/etc.
	d.Instances.Mu.Lock()
	currentState := instance.Status
	d.Instances.Mu.Unlock()
	if !vm.IsValidTransition(currentState, initialState) {
		slog.Warn("Instance in incorrect state for "+strings.ToLower(action),
			"instanceId", instance.ID, "currentState", string(currentState))
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

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

	// Validate and filter instances if specific instance IDs were requested
	instanceIDFilter := make(map[string]bool)
	if len(describeInstancesInput.InstanceIds) > 0 {
		for _, id := range describeInstancesInput.InstanceIds {
			if id != nil && *id != "" {
				if !strings.HasPrefix(*id, "i-") {
					if err := msg.Respond(utils.GenerateErrorPayload(awserrors.ErrorInvalidInstanceIDMalformed)); err != nil {
						slog.Error("Failed to respond to NATS request", "err", err)
					}
					return
				}
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
		slog.Error("handleEC2StartStoppedInstance: instance type not available on this node",
			"instanceId", req.InstanceID, "instanceType", instance.InstanceType)
		respondWithError(awserrors.ErrorInsufficientInstanceCapacity)
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

// terminateStoppedInstanceRequest is the payload for ec2.terminate topic
type terminateStoppedInstanceRequest struct {
	InstanceID string `json:"instance_id"`
}

// handleEC2TerminateStoppedInstance picks up a stopped instance from shared KV,
// deletes its volumes, and removes it from shared KV.
func (d *Daemon) handleEC2TerminateStoppedInstance(msg *nats.Msg) {
	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	var req terminateStoppedInstanceRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to unmarshal request", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if req.InstanceID == "" {
		slog.Error("handleEC2TerminateStoppedInstance: missing instance_id")
		respondWithError(awserrors.ErrorMissingParameter)
		return
	}

	if d.jsManager == nil {
		slog.Error("handleEC2TerminateStoppedInstance: JetStream not available")
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	// Load instance from shared KV
	instance, err := d.jsManager.LoadStoppedInstance(req.InstanceID)
	if err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to load stopped instance", "instanceId", req.InstanceID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2TerminateStoppedInstance: instance not found in shared KV", "instanceId", req.InstanceID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2TerminateStoppedInstance: instance not in stopped state", "instanceId", req.InstanceID, "status", instance.Status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Delete volumes — no QEMU shutdown or unmount needed (already done during stop)
	instance.EBSRequests.Mu.Lock()
	for _, ebsRequest := range instance.EBSRequests.Requests {
		// Internal volumes (EFI, cloud-init) are always cleaned up via ebs.delete
		if ebsRequest.EFI || ebsRequest.CloudInit {
			ebsDeleteData, err := json.Marshal(config.EBSDeleteRequest{Volume: ebsRequest.Name})
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
		})
		if err != nil {
			slog.Error("handleEC2TerminateStoppedInstance: failed to delete volume", "name", ebsRequest.Name, "err", err)
		}
	}
	instance.EBSRequests.Mu.Unlock()

	// Remove from shared KV
	if err := d.jsManager.DeleteStoppedInstance(req.InstanceID); err != nil {
		slog.Error("handleEC2TerminateStoppedInstance: failed to delete from shared KV", "instanceId", req.InstanceID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Info("Terminated stopped instance from shared KV", "instanceId", req.InstanceID)

	if err := msg.Respond(fmt.Appendf(nil, `{"status":"terminated","instanceId":"%s"}`, req.InstanceID)); err != nil {
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

// handleEC2ModifyInstanceAttribute modifies attributes of a stopped instance in shared KV.
// All supported attributes (InstanceType, UserData) require the instance to be stopped.
func (d *Daemon) handleEC2ModifyInstanceAttribute(msg *nats.Msg) {
	respondWithError := func(errCode string) {
		if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
	}

	var input ec2.ModifyInstanceAttributeInput
	if err := json.Unmarshal(msg.Data, &input); err != nil {
		slog.Error("handleEC2ModifyInstanceAttribute: failed to unmarshal request", "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		slog.Error("handleEC2ModifyInstanceAttribute: missing instance_id")
		respondWithError(awserrors.ErrorMissingParameter)
		return
	}

	instanceID := *input.InstanceId

	if d.jsManager == nil {
		slog.Error("handleEC2ModifyInstanceAttribute: JetStream not available")
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	instance, err := d.jsManager.LoadStoppedInstance(instanceID)
	if err != nil {
		slog.Error("handleEC2ModifyInstanceAttribute: failed to load stopped instance", "instanceId", instanceID, "err", err)
		respondWithError(awserrors.ErrorServerInternal)
		return
	}
	if instance == nil {
		slog.Warn("handleEC2ModifyInstanceAttribute: instance not found in shared KV", "instanceId", instanceID)
		respondWithError(awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	if instance.Status != vm.StateStopped {
		slog.Error("handleEC2ModifyInstanceAttribute: instance not in stopped state", "instanceId", instanceID, "status", instance.Status)
		respondWithError(awserrors.ErrorIncorrectInstanceState)
		return
	}

	// Apply the requested attribute change
	if input.InstanceType != nil && input.InstanceType.Value != nil {
		newType := *input.InstanceType.Value
		if newType == "" {
			slog.Error("handleEC2ModifyInstanceAttribute: empty instance type value", "instanceId", instanceID)
			respondWithError(awserrors.ErrorInvalidInstanceAttributeValue)
			return
		}
		if instance.Instance == nil {
			slog.Error("handleEC2ModifyInstanceAttribute: instance.Instance is nil, data integrity issue", "instanceId", instanceID)
			respondWithError(awserrors.ErrorServerInternal)
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
		respondWithError(awserrors.ErrorServerInternal)
		return
	}

	slog.Info("handleEC2ModifyInstanceAttribute: completed successfully", "instanceId", instanceID)

	if err := msg.Respond([]byte(`{}`)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}
