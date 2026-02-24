package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/vm"
)

const (
	maxRestartsInWindow = 3
	restartWindow       = 10 * time.Minute
	restartBackoffBase  = 5 * time.Second
	restartBackoffMax   = 2 * time.Minute
)

// restartBackoff computes the exponential backoff delay for the given
// restart count. Pure function — no side effects.
func restartBackoff(restartCount int) time.Duration {
	delay := restartBackoffBase
	for range restartCount {
		delay *= 2
		if delay > restartBackoffMax {
			return restartBackoffMax
		}
	}
	return delay
}

// classifyCrashReason extracts a human-readable crash reason from the error
// returned by cmd.Wait(). Uses exec.ExitError + syscall.WaitStatus to
// distinguish OOM kills (SIGKILL), segfaults (SIGSEGV), etc.
func classifyCrashReason(waitErr error) string {
	if waitErr == nil {
		return "clean-exit"
	}

	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		return "unknown"
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return "unknown"
	}

	if status.Signaled() {
		switch status.Signal() {
		case syscall.SIGKILL:
			return "oom-killed"
		case syscall.SIGSEGV:
			return "segfault"
		case syscall.SIGABRT:
			return "abort"
		default:
			return fmt.Sprintf("signal-%d", status.Signal())
		}
	}

	if status.Exited() {
		return fmt.Sprintf("exit-%d", status.ExitStatus())
	}

	return "unknown"
}

// handleInstanceCrash is called by the QEMU launch goroutine after cmd.Wait()
// returns during runtime (after startup was confirmed successful). It detects
// the crash reason, transitions the instance to error state, cleans up resources,
// and schedules a restart if policy allows.
func (d *Daemon) handleInstanceCrash(instance *vm.VM, waitErr error) {
	// Guard: if instance is not running, this was an expected exit
	// (stopInstance/terminateInstance set status before QEMU exits)
	d.Instances.Mu.Lock()
	status := instance.Status
	d.Instances.Mu.Unlock()

	if status != vm.StateRunning {
		slog.Debug("QEMU exited but instance not in running state, skipping crash handler",
			"instance", instance.ID, "status", status)
		return
	}

	// Guard: coordinated shutdown in progress
	if d.shuttingDown.Load() {
		slog.Debug("QEMU exited during coordinated shutdown, skipping crash handler",
			"instance", instance.ID)
		return
	}

	reason := classifyCrashReason(waitErr)
	slog.Error("VM process crashed", "instance", instance.ID, "reason", reason, "err", waitErr)

	// Transition to error state
	if err := d.TransitionState(instance, vm.StateError); err != nil {
		slog.Error("Failed to transition crashed instance to error state",
			"instance", instance.ID, "err", err)
	}

	// Update health tracking
	now := time.Now()
	d.Instances.Mu.Lock()
	instance.Health.CrashCount++
	instance.Health.LastCrashTime = now
	instance.Health.LastCrashReason = reason
	if instance.Health.FirstCrashTime.IsZero() {
		instance.Health.FirstCrashTime = now
	}
	instance.Running = false
	instance.PID = 0
	d.Instances.Mu.Unlock()

	// Deallocate resources to fix phantom reservation
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if ok && instanceType != nil {
		slog.Info("Deallocating resources for crashed instance",
			"instance", instance.ID, "type", instance.InstanceType)
		d.resourceMgr.deallocate(instanceType)
	}

	// Clean up stale QMP socket so QEMU can rebind on restart
	if instance.Config.QMPSocket != "" {
		_ = os.Remove(instance.Config.QMPSocket)
	}

	// Unmount EBS volumes (same pattern as stopInstance)
	d.unmountInstanceVolumes(instance)

	// Persist state
	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after crash handling",
			"instance", instance.ID, "err", err)
	}

	d.maybeRestartInstance(instance)
}

// unmountInstanceVolumes sends NATS unmount requests for all volumes attached
// to the instance and updates their state to "available".
func (d *Daemon) unmountInstanceVolumes(instance *vm.VM) {
	instance.EBSRequests.Mu.Lock()
	defer instance.EBSRequests.Mu.Unlock()

	for _, ebsRequest := range instance.EBSRequests.Requests {
		ebsUnMountRequest, err := json.Marshal(ebsRequest)
		if err != nil {
			slog.Error("Failed to marshal volume payload for crash cleanup",
				"err", err)
			continue
		}

		msg, err := d.natsConn.Request(d.ebsTopic("unmount"), ebsUnMountRequest, 30*time.Second)
		if err != nil {
			slog.Error("Failed to unmount volume after crash",
				"name", ebsRequest.Name, "instance", instance.ID, "err", err)
		} else {
			slog.Info("Unmounted volume after crash",
				"instance", instance.ID, "volume", ebsRequest.Name, "data", string(msg.Data))
		}

		// Update user-visible volume state to "available"
		if !ebsRequest.EFI && !ebsRequest.CloudInit {
			if err := d.volumeService.UpdateVolumeState(ebsRequest.Name, "available", "", ""); err != nil {
				slog.Error("Failed to update volume state to available after crash",
					"volumeId", ebsRequest.Name, "err", err)
			}
		}
	}
}

// maybeRestartInstance checks restart policy and schedules a restart if allowed.
func (d *Daemon) maybeRestartInstance(instance *vm.VM) {
	if d.shuttingDown.Load() {
		slog.Info("Skipping restart during shutdown", "instance", instance.ID)
		return
	}

	now := time.Now()

	d.Instances.Mu.Lock()
	health := &instance.Health

	// If crashes are outside the restart window, reset counters
	if !health.FirstCrashTime.IsZero() && now.Sub(health.FirstCrashTime) > restartWindow {
		slog.Info("Crash window expired, resetting counters", "instance", instance.ID)
		health.CrashCount = 1
		health.FirstCrashTime = now
		health.RestartCount = 0
	}

	// Check if we've exceeded the max restarts in the window
	if health.CrashCount > maxRestartsInWindow {
		d.Instances.Mu.Unlock()
		slog.Error("Instance exceeded max restarts in window, leaving in error state",
			"instance", instance.ID,
			"crashes", health.CrashCount,
			"window", restartWindow,
			"max", maxRestartsInWindow)
		return
	}

	restartCount := health.RestartCount
	d.Instances.Mu.Unlock()

	// Check resource availability
	instanceType, ok := d.resourceMgr.instanceTypes[instance.InstanceType]
	if !ok || instanceType == nil {
		slog.Error("Unknown instance type, cannot restart",
			"instance", instance.ID, "type", instance.InstanceType)
		return
	}

	if d.resourceMgr.canAllocate(instanceType, 1) < 1 {
		slog.Error("Insufficient resources to restart instance",
			"instance", instance.ID, "type", instance.InstanceType)
		return
	}

	delay := restartBackoff(restartCount)

	slog.Info("Scheduling instance restart",
		"instance", instance.ID,
		"delay", delay,
		"restartCount", restartCount+1)

	time.AfterFunc(delay, func() {
		d.restartCrashedInstance(instance)
	})
}

// restartCrashedInstance re-verifies the instance is still in error state
// and relaunches it via LaunchInstance.
func (d *Daemon) restartCrashedInstance(instance *vm.VM) {
	d.Instances.Mu.Lock()
	if instance.Status != vm.StateError {
		d.Instances.Mu.Unlock()
		slog.Info("Instance no longer in error state, skipping restart",
			"instance", instance.ID, "status", instance.Status)
		return
	}

	if d.shuttingDown.Load() {
		d.Instances.Mu.Unlock()
		slog.Info("Daemon shutting down, skipping restart", "instance", instance.ID)
		return
	}

	instance.Health.RestartCount++
	d.Instances.Mu.Unlock()

	slog.Info("Restarting crashed instance",
		"instance", instance.ID,
		"restartCount", instance.Health.RestartCount)

	// Transition Error → Pending (valid now that we added it to the transitions map)
	if err := d.TransitionState(instance, vm.StatePending); err != nil {
		slog.Error("Failed to transition instance to pending for restart",
			"instance", instance.ID, "err", err)
		return
	}

	// LaunchInstance handles the full relaunch flow:
	// check PID → MountVolumes → StartInstance → CreateQMPClient → NATS subscribe → Running
	if err := d.LaunchInstance(instance); err != nil {
		slog.Error("Failed to restart crashed instance",
			"instance", instance.ID, "err", err)
		if err := d.TransitionState(instance, vm.StateError); err != nil {
			slog.Error("Failed to transition instance back to error after restart failure",
				"instance", instance.ID, "err", err)
		}
	}
}
