package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

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

// onInstanceUpHook returns the daemon's OnInstanceUp callback. Subscribing
// per-instance NATS topics is the only side-effect; the manager fires this
// synchronously after a successful Pending→Running transition.
func (d *Daemon) onInstanceUpHook() func(*vm.VM) {
	return func(instance *vm.VM) {
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
			return
		}
		d.natsSubscriptions[instance.ID] = sub

		consoleSub, err := d.natsConn.Subscribe(
			fmt.Sprintf("ec2.%s.GetConsoleOutput", instance.ID),
			d.handleEC2GetConsoleOutput,
		)
		if err != nil {
			slog.Error("OnInstanceUp: failed to subscribe to console output topic",
				"instanceId", instance.ID, "err", err)
			return
		}
		d.natsSubscriptions[consoleSubKey] = consoleSub
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
		Hooks: vm.ManagerHooks{
			OnInstanceUp:   d.onInstanceUpHook(),
			OnInstanceDown: d.onInstanceDownHook(),
		},
		ShutdownSignal:     d.shuttingDown.Load,
		CrashHandler:       d.handleInstanceCrash,
		TransitionState:    d.TransitionState,
		MarkInstanceFailed: d.markInstanceFailed,
		DevNetworking:      d.config.Daemon.DevNetworking,
		BindHost:           d.config.Host,
	}
}
