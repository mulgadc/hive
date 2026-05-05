package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_placementgroup "github.com/mulgadc/spinifex/spinifex/handlers/ec2/placementgroup"
	"github.com/mulgadc/spinifex/spinifex/qmp"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/nats-io/nats.go"
)

// Thin adapters that satisfy the vm.Manager collaborator interfaces. The
// bodies delegate to existing daemon methods or services; the manager
// receives them via vm.Deps so it can drive lifecycle without importing the
// daemon package.

// stateStoreAdapter satisfies vm.StateStore by delegating to the cluster
// JetStream manager. It also tolerates a nil JetStreamManager during early
// boot so manager construction can happen before initJetStream returns.
type stateStoreAdapter struct {
	js *JetStreamManager
}

var _ vm.StateStore = (*stateStoreAdapter)(nil)

func newStateStoreAdapter(js *JetStreamManager) *stateStoreAdapter {
	return &stateStoreAdapter{js: js}
}

func (a *stateStoreAdapter) SaveRunningState(nodeID string, snapshot map[string]*vm.VM) error {
	return a.js.WriteState(nodeID, snapshot)
}

func (a *stateStoreAdapter) LoadRunningState(nodeID string) (map[string]*vm.VM, error) {
	return a.js.LoadState(nodeID)
}

func (a *stateStoreAdapter) WriteStoppedInstance(id string, v *vm.VM) error {
	return a.js.WriteStoppedInstance(id, v)
}

func (a *stateStoreAdapter) LoadStoppedInstance(id string) (*vm.VM, error) {
	return a.js.LoadStoppedInstance(id)
}

func (a *stateStoreAdapter) DeleteStoppedInstance(id string) error {
	return a.js.DeleteStoppedInstance(id)
}

func (a *stateStoreAdapter) ListStoppedInstances() ([]*vm.VM, error) {
	return a.js.ListStoppedInstances()
}

func (a *stateStoreAdapter) WriteTerminatedInstance(id string, v *vm.VM) error {
	return a.js.WriteTerminatedInstance(id, v)
}

func (a *stateStoreAdapter) ListTerminatedInstances() ([]*vm.VM, error) {
	return a.js.ListTerminatedInstances()
}

// volumeMounterAdapter satisfies vm.VolumeMounter by routing ebs.mount /
// ebs.unmount NATS requests, mirroring Daemon.MountVolumes and
// Daemon.unmountInstanceVolumes. The mount path mutates
// instance.EBSRequests.Requests[k].NBDURI; unmount also flips boot/data
// volumes back to "available" via VolumeStateUpdater so the AWS API view
// stays consistent.
type volumeMounterAdapter struct {
	nc       *nats.Conn
	node     string
	volState vm.VolumeStateUpdater
}

var _ vm.VolumeMounter = (*volumeMounterAdapter)(nil)

func newVolumeMounterAdapter(nc *nats.Conn, node string, volState vm.VolumeStateUpdater) *volumeMounterAdapter {
	return &volumeMounterAdapter{nc: nc, node: node, volState: volState}
}

func (a *volumeMounterAdapter) topic(action string) string {
	return fmt.Sprintf("ebs.%s.%s", a.node, action)
}

func (a *volumeMounterAdapter) Mount(instance *vm.VM) error {
	instance.EBSRequests.Mu.Lock()
	defer instance.EBSRequests.Mu.Unlock()

	for k, v := range instance.EBSRequests.Requests {
		ebsMountRequest, err := json.Marshal(v)
		if err != nil {
			slog.Error("Failed to marshal volume payload", "err", err)
			return err
		}

		reply, err := a.nc.Request(a.topic("mount"), ebsMountRequest, 30*time.Second)

		slog.Info("Mounting volume", "Vol", v.Name, "NBDURI", v.NBDURI)

		if err != nil {
			slog.Error("Failed to request EBS mount", "err", err)
			return err
		}

		var ebsMountResponse types.EBSMountResponse
		if err := json.Unmarshal(reply.Data, &ebsMountResponse); err != nil {
			slog.Error("Failed to unmarshal volume response:", "err", err)
			return err
		}

		if ebsMountResponse.Error != "" {
			slog.Error("Failed to mount volume", "error", ebsMountResponse.Error)
			return fmt.Errorf("failed to mount volume: %s", ebsMountResponse.Error)
		}

		slog.Debug("Mounted volume successfully", "response", ebsMountResponse.URI)
		instance.EBSRequests.Requests[k].NBDURI = ebsMountResponse.URI
	}

	return nil
}

func (a *volumeMounterAdapter) Unmount(instance *vm.VM) error {
	instance.EBSRequests.Mu.Lock()
	defer instance.EBSRequests.Mu.Unlock()

	for _, ebsRequest := range instance.EBSRequests.Requests {
		ebsUnMountRequest, err := json.Marshal(ebsRequest)
		if err != nil {
			slog.Error("Failed to marshal volume payload for unmount", "err", err)
			continue
		}

		msg, err := a.nc.Request(a.topic("unmount"), ebsUnMountRequest, 30*time.Second)
		if err != nil {
			slog.Error("Failed to unmount volume",
				"name", ebsRequest.Name, "instance", instance.ID, "err", err)
		} else {
			slog.Info("Unmounted volume",
				"instance", instance.ID, "volume", ebsRequest.Name, "data", string(msg.Data))
		}

		if !ebsRequest.EFI && !ebsRequest.CloudInit && a.volState != nil {
			if err := a.volState.UpdateVolumeState(ebsRequest.Name, "available", "", ""); err != nil {
				slog.Error("Failed to update volume state to available after unmount",
					"volumeId", ebsRequest.Name, "err", err)
			}
		}
	}

	return nil
}

// MountOne sends ebs.mount for a single request and writes the resolved
// NBDURI back into req.NBDURI. Used by hot-attach (Manager.AttachVolume).
func (a *volumeMounterAdapter) MountOne(req *types.EBSRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal ebs.mount request: %w", err)
	}

	reply, err := a.nc.Request(a.topic("mount"), payload, 30*time.Second)
	if err != nil {
		return fmt.Errorf("ebs.mount NATS request: %w", err)
	}

	var resp types.EBSMountResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		return fmt.Errorf("unmarshal ebs.mount response: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("ebs.mount returned error: %s", resp.Error)
	}
	if resp.URI == "" {
		return vm.ErrMountAmbiguous
	}

	req.NBDURI = resp.URI
	return nil
}

// UnmountOne sends ebs.unmount for a single request. Best-effort: errors
// are logged. Mirrors the pre-2d Daemon.rollbackEBSMount semantics so
// AttachVolume rollback and DetachVolume Phase 3 share one code path.
func (a *volumeMounterAdapter) UnmountOne(req types.EBSRequest) {
	payload, err := json.Marshal(req)
	if err != nil {
		slog.Error("UnmountOne: failed to marshal unmount request",
			"volume", req.Name, "err", err)
		return
	}
	msg, err := a.nc.Request(a.topic("unmount"), payload, 10*time.Second)
	if err != nil {
		slog.Error("UnmountOne: ebs.unmount NATS request failed",
			"volume", req.Name, "err", err)
		return
	}
	var resp types.EBSUnMountResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		slog.Error("UnmountOne: failed to unmarshal response",
			"volume", req.Name, "err", err)
		return
	}
	if resp.Error != "" {
		slog.Error("UnmountOne: ebs.unmount returned error",
			"volume", req.Name, "err", resp.Error)
		return
	}
	if resp.Mounted {
		slog.Error("UnmountOne: volume still mounted after unmount", "volume", req.Name)
		return
	}
	slog.Info("UnmountOne: volume unmounted successfully", "volume", req.Name)
}

// qmpClientFactoryAdapter satisfies vm.QMPClientFactory. It performs only
// the connect + qmp_capabilities handshake; the manager owns starting the
// heartbeat goroutine on the returned client.
type qmpClientFactoryAdapter struct{}

var _ vm.QMPClientFactory = (*qmpClientFactoryAdapter)(nil)

func newQMPClientFactoryAdapter() *qmpClientFactoryAdapter { return &qmpClientFactoryAdapter{} }

func (a *qmpClientFactoryAdapter) Create(v *vm.VM) (*qmp.QMPClient, error) {
	client, err := qmp.NewQMPClient(v.Config.QMPSocket)
	if err != nil {
		return nil, fmt.Errorf("connect QMP socket %s: %w", v.Config.QMPSocket, err)
	}

	if _, err := sendQMPHandshake(client, v.ID); err != nil {
		_ = client.Conn.Close()
		return nil, err
	}

	return client, nil
}

// sendQMPHandshake issues qmp_capabilities under the QMPClient's mutex. Mirrors
// the slim version of Daemon.SendQMPCommand for the handshake; the daemon's
// full helper still owns long-running command dispatch.
func sendQMPHandshake(client *qmp.QMPClient, instanceID string) (*qmp.QMPResponse, error) {
	if client == nil || client.Encoder == nil || client.Decoder == nil {
		return nil, errors.New("QMP client is not initialized")
	}

	client.Mu.Lock()
	defer client.Mu.Unlock()

	if err := client.Conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	defer func() { _ = client.Conn.SetReadDeadline(time.Time{}) }()

	if err := client.Encoder.Encode(qmp.QMPCommand{Execute: "qmp_capabilities"}); err != nil {
		return nil, fmt.Errorf("encode qmp_capabilities: %w", err)
	}

	for {
		var msg map[string]any
		if err := client.Decoder.Decode(&msg); err != nil {
			return nil, fmt.Errorf("decode qmp_capabilities response: %w", err)
		}
		if _, ok := msg["event"]; ok {
			continue
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal qmp response: %w", err)
		}
		var resp qmp.QMPResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal qmp response: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("qmp_capabilities failed: %s", resp.Error.Desc)
		}
		slog.Debug("QMP handshake complete", "instance", instanceID)
		return &resp, nil
	}
}

// processLauncherAdapter satisfies vm.ProcessLauncher. The real
// implementation is a 1:1 wrapper over Config.Execute; the seam exists so
// tests can substitute scripted commands.
type processLauncherAdapter struct{}

var _ vm.ProcessLauncher = (*processLauncherAdapter)(nil)

func newProcessLauncherAdapter() *processLauncherAdapter { return &processLauncherAdapter{} }

func (a *processLauncherAdapter) Launch(cfg *vm.Config) (*exec.Cmd, error) {
	return cfg.Execute()
}

// instanceTypeResolverAdapter satisfies vm.InstanceTypeResolver. It looks up
// the EC2 InstanceTypeInfo on the daemon's ResourceManager and projects the
// QEMU-relevant numbers (vCPU, memory MiB, architecture) into the
// SDK-agnostic vm.InstanceTypeSpec.
type instanceTypeResolverAdapter struct {
	rm *ResourceManager
}

var _ vm.InstanceTypeResolver = (*instanceTypeResolverAdapter)(nil)

func newInstanceTypeResolverAdapter(rm *ResourceManager) *instanceTypeResolverAdapter {
	return &instanceTypeResolverAdapter{rm: rm}
}

func (a *instanceTypeResolverAdapter) Resolve(name string) (vm.InstanceTypeSpec, bool) {
	it := a.rm.instanceTypes[name]
	if it == nil {
		return vm.InstanceTypeSpec{}, false
	}
	architecture := "x86_64"
	if it.ProcessorInfo != nil && len(it.ProcessorInfo.SupportedArchitectures) > 0 && it.ProcessorInfo.SupportedArchitectures[0] != nil {
		architecture = *it.ProcessorInfo.SupportedArchitectures[0]
	}
	return vm.InstanceTypeSpec{
		VCPUs:        int(instanceTypeVCPUs(it)),
		MemoryMiB:    int(instanceTypeMemoryMiB(it)),
		Architecture: architecture,
	}, true
}

// resourceControllerAdapter satisfies vm.ResourceController. Both Allocate
// and Deallocate look up the EC2 InstanceTypeInfo by name before delegating
// to the existing capacity-keyed resource manager methods.
type resourceControllerAdapter struct {
	rm *ResourceManager
}

var _ vm.ResourceController = (*resourceControllerAdapter)(nil)

func newResourceControllerAdapter(rm *ResourceManager) *resourceControllerAdapter {
	return &resourceControllerAdapter{rm: rm}
}

func (a *resourceControllerAdapter) Allocate(instanceType string) error {
	it := a.rm.instanceTypes[instanceType]
	if it == nil {
		return fmt.Errorf("instance type %s not found", instanceType)
	}
	return a.rm.allocate(it)
}

func (a *resourceControllerAdapter) Deallocate(instanceType string) {
	it := a.rm.instanceTypes[instanceType]
	if it == nil {
		return
	}
	a.rm.deallocate(it)
}

func (a *resourceControllerAdapter) CanAllocate(instanceType string, count int) int {
	it := a.rm.instanceTypes[instanceType]
	if it == nil {
		return 0
	}
	return a.rm.canAllocate(it, count)
}

// volumeStateUpdaterAdapter satisfies vm.VolumeStateUpdater by delegating to
// the daemon's volume service.
type volumeStateUpdaterAdapter struct {
	svc volumeStateUpdater
}

// volumeStateUpdater is the narrow slice of handlers_ec2_volume.VolumeServiceImpl
// that the manager touches. Defining it locally avoids dragging the full
// volume-service surface into the adapter.
type volumeStateUpdater interface {
	UpdateVolumeState(volumeID, state, instanceID, attachmentDevice string) error
}

var _ vm.VolumeStateUpdater = (*volumeStateUpdaterAdapter)(nil)

func newVolumeStateUpdaterAdapter(svc volumeStateUpdater) *volumeStateUpdaterAdapter {
	return &volumeStateUpdaterAdapter{svc: svc}
}

func (a *volumeStateUpdaterAdapter) UpdateVolumeState(volumeID, state, instanceID, attachmentDevice string) error {
	return a.svc.UpdateVolumeState(volumeID, state, instanceID, attachmentDevice)
}

// onInstanceRecoveringHook returns the daemon's OnInstanceRecovering
// callback. Restore fires it once per instance about to be relaunched so
// concurrent terminate commands can land on this node before launch
// completes — without it, ec2.cmd.<id> would only be subscribed by
// onInstanceUpHook after launch success, leaving a window where the
// instance is reachable by DescribeInstances (in StatePending) but not
// by EC2 commands. Mirrors the pre-2e early-subscribe block in
// daemon.restoreInstances.
func (d *Daemon) onInstanceRecoveringHook() func(*vm.VM) {
	return func(instance *vm.VM) {
		d.mu.Lock()
		defer d.mu.Unlock()

		if _, ok := d.natsSubscriptions[instance.ID]; ok {
			return
		}
		sub, err := d.natsConn.Subscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), d.handleEC2Events)
		if err != nil {
			slog.Error("OnInstanceRecovering: failed to early-subscribe per-instance topic",
				"instanceId", instance.ID, "err", err)
			return
		}
		d.natsSubscriptions[instance.ID] = sub
	}
}

// consumeCleanShutdownMarker returns the daemon's
// ConsumeCleanShutdownMarker callback. Returns true (and deletes the
// marker) when a clean shutdown was recorded for this node on the
// previous run; returns false otherwise so Restore takes the cautious
// "validate stale PIDs" path.
func (d *Daemon) consumeCleanShutdownMarker() func() bool {
	return func() bool {
		if d.jsManager == nil {
			return false
		}
		marker, err := d.jsManager.ReadShutdownMarker(d.node)
		if err != nil || !marker {
			return false
		}
		slog.Info("Clean shutdown marker found, trusting KV state")
		_ = d.jsManager.DeleteShutdownMarker(d.node)
		return true
	}
}

// onInstanceUpHook returns the daemon's OnInstanceUp callback. Subscribing
// per-instance NATS topics is the only side-effect; the manager fires this
// synchronously after a successful Pending→Running transition. Returns the
// first subscribe error so the manager (specifically reconnectInstance) can
// roll back QMP rather than persist a half-reachable instance to KV.
func (d *Daemon) onInstanceUpHook() func(*vm.VM) error {
	return func(instance *vm.VM) error {
		d.mu.Lock()
		defer d.mu.Unlock()

		if existing, ok := d.natsSubscriptions[instance.ID]; ok {
			_ = existing.Unsubscribe()
		}
		consoleSubKey := instance.ID + ".console"
		if existing, ok := d.natsSubscriptions[consoleSubKey]; ok {
			_ = existing.Unsubscribe()
		}

		sub, err := d.natsConn.Subscribe(fmt.Sprintf("ec2.cmd.%s", instance.ID), d.handleEC2Events)
		if err != nil {
			slog.Error("OnInstanceUp: failed to subscribe to per-instance topic",
				"instanceId", instance.ID, "err", err)
			return fmt.Errorf("subscribe ec2.cmd.%s: %w", instance.ID, err)
		}
		d.natsSubscriptions[instance.ID] = sub

		consoleSub, err := d.natsConn.Subscribe(
			fmt.Sprintf("ec2.%s.GetConsoleOutput", instance.ID),
			d.handleEC2GetConsoleOutput,
		)
		if err != nil {
			slog.Error("OnInstanceUp: failed to subscribe to console output topic",
				"instanceId", instance.ID, "err", err)
			return fmt.Errorf("subscribe ec2.%s.GetConsoleOutput: %w", instance.ID, err)
		}
		d.natsSubscriptions[consoleSubKey] = consoleSub

		// Re-claim GPU after a daemon restart with a still-running QEMU
		// process: the manager's reconnect path fires OnInstanceUp without
		// going through the handler-side Claim, so the GPU pool would
		// otherwise treat the slot as free. ReclaimByAddress is a no-op
		// when the same instance already owns the slot, so the launch and
		// start-stopped paths (which Claim before Run) are unaffected.
		if d.gpuManager != nil && instance.GPUPCIAddress != "" {
			if err := d.gpuManager.ReclaimByAddress(instance.GPUPCIAddress, instance.ID); err != nil {
				slog.Warn("Failed to re-claim GPU on instance up",
					"gpu", instance.GPUPCIAddress, "instanceId", instance.ID, "err", err)
			}
		}
		return nil
	}
}

// onInstanceDownHook returns the daemon's OnInstanceDown callback. It
// unsubscribes the per-instance NATS topics that onInstanceUpHook
// registered.
func (d *Daemon) onInstanceDownHook() func(string) {
	return func(instanceID string) {
		d.mu.Lock()
		defer d.mu.Unlock()

		if sub, ok := d.natsSubscriptions[instanceID]; ok {
			if err := sub.Unsubscribe(); err != nil {
				slog.Error("OnInstanceDown: failed to unsubscribe instance topic",
					"instanceId", instanceID, "err", err)
			}
			delete(d.natsSubscriptions, instanceID)
		}
		consoleSubKey := instanceID + ".console"
		if sub, ok := d.natsSubscriptions[consoleSubKey]; ok {
			if err := sub.Unsubscribe(); err != nil {
				slog.Error("OnInstanceDown: failed to unsubscribe console topic",
					"instanceId", instanceID, "err", err)
			}
			delete(d.natsSubscriptions, consoleSubKey)
		}
	}
}

// buildVMManagerDeps assembles the vm.Deps struct for the running daemon.
// All collaborators must already be initialized; callers are expected to
// invoke this from Daemon.Start after services and JetStream are ready.
func (d *Daemon) buildVMManagerDeps() vm.Deps {
	volState := newVolumeStateUpdaterAdapter(d.volumeService)
	return vm.Deps{
		NodeID:             d.node,
		StateStore:         newStateStoreAdapter(d.jsManager),
		VolumeMounter:      newVolumeMounterAdapter(d.natsConn, d.node, volState),
		QMPClientFactory:   newQMPClientFactoryAdapter(),
		ProcessLauncher:    newProcessLauncherAdapter(),
		NetworkPlumber:     d.networkPlumber,
		InstanceTypes:      newInstanceTypeResolverAdapter(d.resourceMgr),
		Resources:          newResourceControllerAdapter(d.resourceMgr),
		VolumeStateUpdater: volState,
		InstanceCleaner:    newInstanceCleanerAdapter(d),
		Hooks: vm.ManagerHooks{
			OnInstanceUp:         d.onInstanceUpHook(),
			OnInstanceDown:       d.onInstanceDownHook(),
			OnInstanceRecovering: d.onInstanceRecoveringHook(),
		},
		ShutdownSignal:             d.shuttingDown.Load,
		CrashHandler:               d.vmMgr.HandleCrash,
		TransitionState:            d.TransitionState,
		DevNetworking:              d.config.Daemon.DevNetworking,
		BindHost:                   d.config.Host,
		DetachDelay:                d.detachDelay,
		ConsumeCleanShutdownMarker: d.consumeCleanShutdownMarker(),
	}
}

// instanceCleanerAdapter satisfies vm.InstanceCleaner by delegating to the
// daemon's existing volume/VPC/EIP/placement-group services. The manager
// owns the QMP and tap teardown directly; this adapter covers the
// AWS-resource cleanup steps that require service access.
type instanceCleanerAdapter struct {
	d *Daemon
}

var _ vm.InstanceCleaner = (*instanceCleanerAdapter)(nil)

func newInstanceCleanerAdapter(d *Daemon) *instanceCleanerAdapter {
	return &instanceCleanerAdapter{d: d}
}

// DeleteVolumes deletes EFI / cloud-init internal volumes via ebs.delete
// and user volumes flagged DeleteOnTermination via the volume service.
// Errors are logged per volume; partial failure is tolerated to match
// pre-2c stopInstance behaviour.
func (a *instanceCleanerAdapter) DeleteVolumes(instance *vm.VM) {
	instance.EBSRequests.Mu.Lock()
	defer instance.EBSRequests.Mu.Unlock()

	for _, ebsRequest := range instance.EBSRequests.Requests {
		// Internal volumes (EFI, cloud-init) always go through ebs.delete to
		// stop their viperblockd processes. S3 data is cleaned up via the
		// parent root volume's DeleteVolume (which removes -efi/ and
		// -cloudinit/ prefixes).
		if ebsRequest.EFI || ebsRequest.CloudInit {
			ebsDeleteData, err := json.Marshal(types.EBSDeleteRequest{Volume: ebsRequest.Name})
			if err != nil {
				slog.Error("Failed to marshal ebs.delete request for internal volume",
					"name", ebsRequest.Name, "err", err)
				continue
			}
			deleteMsg, err := a.d.natsConn.Request("ebs.delete", ebsDeleteData, 30*time.Second)
			if err != nil {
				slog.Warn("Failed to send ebs.delete for internal volume",
					"name", ebsRequest.Name, "id", instance.ID, "err", err)
			} else {
				slog.Info("Sent ebs.delete for internal volume",
					"name", ebsRequest.Name, "id", instance.ID, "data", string(deleteMsg.Data))
			}
			continue
		}

		// User-visible volumes: only delete when DeleteOnTermination is set.
		if !ebsRequest.DeleteOnTermination {
			slog.Info("Volume has DeleteOnTermination=false, skipping deletion",
				"name", ebsRequest.Name, "id", instance.ID)
			continue
		}

		slog.Info("Deleting volume with DeleteOnTermination=true",
			"name", ebsRequest.Name, "id", instance.ID)
		if a.d.volumeService == nil {
			slog.Warn("Volume service not configured, cannot delete volume",
				"name", ebsRequest.Name, "id", instance.ID)
			continue
		}
		if _, err := a.d.volumeService.DeleteVolume(&ec2.DeleteVolumeInput{
			VolumeId: &ebsRequest.Name,
		}, instance.AccountID); err != nil {
			slog.Error("Failed to delete volume on termination",
				"name", ebsRequest.Name, "id", instance.ID, "err", err)
		} else {
			slog.Info("Deleted volume on termination",
				"name", ebsRequest.Name, "id", instance.ID)
		}
	}
}

// CleanupMgmtNetwork tears down the management TAP device (derived from
// instance.ID so unsetup instances are tolerated) and releases the
// management IP allocation if the daemon has one.
func (a *instanceCleanerAdapter) CleanupMgmtNetwork(instance *vm.VM) {
	mgmtTap := MgmtTapName(instance.ID)
	if err := CleanupMgmtTapDevice(mgmtTap); err != nil {
		slog.Warn("Failed to clean up mgmt tap device",
			"tap", mgmtTap, "instanceId", instance.ID, "err", err)
	}
	if a.d.mgmtIPAllocator != nil {
		a.d.mgmtIPAllocator.Release(instance.ID)
	}
}

// ReleasePublicIP publishes vpc.delete-nat for the OVN dnat_and_snat rule
// and releases the public IP back to the external IPAM pool. No-op when
// the instance has no public IP.
func (a *instanceCleanerAdapter) ReleasePublicIP(instance *vm.VM) {
	if instance.PublicIP == "" || instance.PublicIPPool == "" || a.d.externalIPAM == nil {
		return
	}

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
	a.d.publishNATEvent("vpc.delete-nat", vpcId, instance.PublicIP, logicalIP, portName, "")

	if err := a.d.externalIPAM.ReleaseIP(instance.PublicIPPool, instance.PublicIP); err != nil {
		slog.Warn("Failed to release public IP on termination",
			"ip", instance.PublicIP, "pool", instance.PublicIPPool, "err", err)
	} else {
		slog.Info("Released public IP on termination",
			"ip", instance.PublicIP, "instanceId", instance.ID)
	}
}

// DetachAndDeleteENI detaches the auto-created ENI from the instance and
// deletes it via the VPC service. NotFound is tolerated. Extra ENIs are
// cleaned up by the load-balancer service via its own teardown loop and
// are not touched here.
func (a *instanceCleanerAdapter) DetachAndDeleteENI(instance *vm.VM) {
	if instance.ENIId == "" || a.d.vpcService == nil {
		return
	}
	if detachErr := a.d.vpcService.DetachENI(instance.AccountID, instance.ENIId); detachErr != nil {
		slog.Warn("Failed to detach ENI on termination",
			"eni", instance.ENIId, "instanceId", instance.ID, "err", detachErr)
	}
	if _, eniErr := a.d.vpcService.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: &instance.ENIId,
	}, instance.AccountID); eniErr != nil {
		if strings.Contains(eniErr.Error(), awserrors.ErrorInvalidNetworkInterfaceIDNotFound) {
			slog.Debug("ENI already cleaned up on termination", "eni", instance.ENIId)
		} else {
			slog.Error("Failed to delete ENI on termination",
				"eni", instance.ENIId, "instanceId", instance.ID, "err", eniErr)
		}
	} else {
		slog.Info("Deleted ENI on termination",
			"eni", instance.ENIId, "instanceId", instance.ID)
	}
}

// RemoveFromPlacementGroup unbinds the instance from its placement group
// if one is set. No-op for ungrouped instances and when the placement
// group service is not configured.
func (a *instanceCleanerAdapter) RemoveFromPlacementGroup(instance *vm.VM) {
	if instance.PlacementGroupName == "" || a.d.placementGroupService == nil {
		return
	}
	if _, err := a.d.placementGroupService.RemoveInstance(&handlers_ec2_placementgroup.RemoveInstanceInput{
		GroupName:  instance.PlacementGroupName,
		NodeName:   instance.PlacementGroupNode,
		InstanceID: instance.ID,
	}, instance.AccountID); err != nil {
		slog.Error("Failed to remove instance from placement group",
			"instanceId", instance.ID, "groupName", instance.PlacementGroupName, "err", err)
	}
}

// ReleaseGPU unbinds the instance's GPU from vfio-pci and rebinds to its
// original host driver. No-op for instances without a GPU allocation or
// when GPU passthrough is disabled.
func (a *instanceCleanerAdapter) ReleaseGPU(instance *vm.VM) {
	if a.d.gpuManager == nil || instance.GPUPCIAddress == "" {
		return
	}
	if err := a.d.gpuManager.Release(instance.ID); err != nil {
		slog.Error("Failed to release GPU on stop, device may need manual rebind",
			"gpu", instance.GPUPCIAddress, "instanceId", instance.ID, "err", err)
		return
	}
	slog.Info("GPU released", "gpu", instance.GPUPCIAddress, "instanceId", instance.ID)
}
