# EC2 Instance Health Monitoring & Auto-Restart

**Status: In Progress** — Phases A (Crash Detection), B (Auto-Restart), D1 (OOM Scores) implemented

Related: [Cluster Lifecycle Plan](heartbeat-cluster-shutdown.md) (Phases 2.1-2.7)

## Summary

Detect QEMU process crashes at runtime (OOM kills, segfaults, unexpected exits) and automatically restart affected VMs. Protect the host from resource exhaustion by enforcing memory/CPU limits on both system services and VM processes. Expose health state through the AWS-compatible `DescribeInstanceStatus` API so clients get standard SystemStatus/InstanceStatus responses matching AWS behavior. This is a prerequisite for production-grade reliability — without it, a single OOM event can silently kill VMs with no recovery.

## Context / Problem Statement

### The OOM Kill Scenario

Real-world example from development:
```
[ 6269.170115] oom-kill:constraint=CONSTRAINT_NONE,...task=qemu-system-x86,pid=37970,uid=1000
[ 6269.171405] Out of memory: Killed process 37970 (qemu-system-x86)
               total-vm:9125308kB, anon-rss:8408868kB
```

The Linux OOM killer silently terminated QEMU. The daemon had no idea — the VM appeared "running" in state until the next manual check. Resources stayed allocated, the instance was unreachable, and no recovery happened.

### Current Gaps

1. **No runtime crash detection**: `cmd.Wait()` completes when QEMU exits (daemon.go:1817), but the exit result channel is only read during the startup handshake (waiting for PID). After that, nobody listens. A crashed QEMU goes unnoticed.

2. **QMP heartbeat doesn't act on failures**: The 30-second QMP heartbeat (daemon.go:1458-1490) sends `query-status` and logs the result. On error, it `continue`s — no state transition, no alert, no restart attempt.

3. **No resource limits on any process**: QEMU, predastore, viperblock, nats — all run as bare processes with no cgroup limits, no OOM score adjustments, no memory caps. A single runaway VM or service can exhaust the host.

4. **Resource accounting leak**: If QEMU crashes without going through `stopInstance()`, the ResourceManager never calls `deallocate()`. The vCPU/memory stay "allocated" — phantom reservations that block new instances from launching.

5. **Daemon restart recovery exists but runtime recovery doesn't**: `restoreInstances()` (daemon.go:770-891) handles the case where the daemon itself restarted and QEMU may have survived or died. But during normal operation, there's no equivalent — QEMU can crash and sit in a broken state indefinitely.

### What Already Works

| Component | File | Status |
|-----------|------|--------|
| QMP heartbeat goroutine (30s `query-status`) | `daemon.go:1458-1490` | Logs only, no action on failure |
| `cmd.Wait()` goroutine per QEMU process | `daemon.go:1817` | Fires on exit, result channel unread after startup |
| `isInstanceProcessRunning()` (signal 0 check) | `daemon.go:893-904` | Used only during recovery, not runtime |
| `restoreInstances()` (daemon restart recovery) | `daemon.go:770-891` | Relaunches crashed VMs after daemon restart |
| `deallocate()` on ResourceManager | `daemon.go:2077-2087` | Called by `stopInstance()`, not by crash handler |
| VM state machine with `StateError` transition | `vm/state.go:41-49` | Exists but rarely used |

---

## Proposed Changes

### Phase A: Runtime Crash Detection

The infrastructure for detection already exists — it just needs to be connected.

#### A1: QEMU Exit Watcher Goroutine

The `cmd.Wait()` in `StartInstance()` (daemon.go:1817) already blocks until QEMU exits. Currently it sends the exit code to a channel that nobody reads after startup. Fix: launch a dedicated watcher goroutine that outlives the startup phase.

```go
// In StartInstance(), after successful QEMU launch:
go d.watchInstanceProcess(instance, cmd, exitCh)
```

```go
func (d *Daemon) watchInstanceProcess(instance *vm.VM, cmd *exec.Cmd, exitCh <-chan error) {
    exitErr := <-exitCh  // blocks until QEMU process exits

    // Check if this was an expected exit (stopInstance/terminate already handled it)
    if d.shuttingDown.Load() {
        return
    }
    instance.Mu.Lock()
    status := instance.Status
    instance.Mu.Unlock()
    if status == vm.StateStopping || status == vm.StateStopped ||
       status == vm.StateShuttingDown || status == vm.StateTerminated {
        return  // expected exit, stopInstance() is handling cleanup
    }

    // Unexpected crash
    slog.Error("QEMU process exited unexpectedly",
        "instance", instance.ID,
        "status", status,
        "exitErr", exitErr,
        "pid", cmd.Process.Pid)

    d.handleInstanceCrash(instance, exitErr)
}
```

#### A2: Crash Handler

```go
func (d *Daemon) handleInstanceCrash(instance *vm.VM, exitErr error) {
    // 1. Transition to error state
    d.TransitionState(instance, vm.StateError)

    // 2. Deallocate resources (fix phantom reservation)
    d.resourceMgr.deallocate(instance)

    // 3. Unmount volumes (cleanup nbdkit)
    d.unmountVolumes(instance)

    // 4. Record crash metadata for observability
    instance.Mu.Lock()
    instance.CrashCount++
    instance.LastCrashTime = time.Now()
    instance.LastCrashReason = classifyCrashReason(exitErr)
    instance.Mu.Unlock()

    // 5. Persist state
    d.WriteState()

    // 6. Attempt auto-restart (if policy allows)
    d.maybeRestartInstance(instance)
}
```

#### A3: Crash Reason Classification

Determine why QEMU died — this informs whether restart is safe:

```go
func classifyCrashReason(exitErr error) string {
    if exitErr == nil {
        return "clean-exit"  // QEMU exited 0 unexpectedly (shouldn't happen)
    }
    var exitError *exec.ExitError
    if errors.As(exitErr, &exitError) {
        status := exitError.Sys().(syscall.WaitStatus)
        if status.Signaled() {
            switch status.Signal() {
            case syscall.SIGKILL:
                return "oom-killed"  // signal 9 = almost certainly OOM
            case syscall.SIGSEGV:
                return "segfault"
            case syscall.SIGABRT:
                return "abort"
            default:
                return fmt.Sprintf("signal-%d", status.Signal())
            }
        }
        return fmt.Sprintf("exit-%d", status.ExitStatus())
    }
    return "unknown"
}
```

Cross-reference with dmesg for OOM confirmation:
```go
// After detecting SIGKILL, check dmesg for OOM kill of this PID
func checkDmesgOOM(pid int) bool {
    out, err := exec.Command("dmesg", "--time-format", "iso", "-T").Output()
    // Search for "oom-kill" + PID in last 60 seconds of dmesg
    // Return true if found
}
```

### Phase B: Auto-Restart Policy

Not all crashes should trigger restart. The policy should prevent restart loops and respect resource constraints.

#### B1: Restart Policy

```go
type RestartPolicy struct {
    MaxRestarts    int           // max restarts before giving up (default: 3)
    RestartWindow  time.Duration // window for counting restarts (default: 10 minutes)
    BackoffBase    time.Duration // initial backoff delay (default: 5 seconds)
    BackoffMax     time.Duration // maximum backoff delay (default: 2 minutes)
    RestartOnOOM   bool          // restart after OOM kill (default: true)
    RestartOnCrash bool          // restart after other crashes (default: true)
}
```

#### B2: Restart Decision

```go
func (d *Daemon) maybeRestartInstance(instance *vm.VM) {
    policy := d.getRestartPolicy()

    // Check restart count within window
    if instance.CrashCount > policy.MaxRestarts {
        recentCrashes := countCrashesSince(instance, time.Now().Add(-policy.RestartWindow))
        if recentCrashes >= policy.MaxRestarts {
            slog.Error("Instance exceeded max restart attempts, leaving in error state",
                "instance", instance.ID,
                "crashes", recentCrashes,
                "window", policy.RestartWindow)
            return
        }
    }

    // Check crash type
    reason := instance.LastCrashReason
    if reason == "oom-killed" && !policy.RestartOnOOM {
        slog.Warn("OOM restart disabled by policy", "instance", instance.ID)
        return
    }

    // Check node mode (don't restart on draining node)
    if d.nodeMode.Load().(string) != "normal" {
        slog.Info("Skipping restart, node not in normal mode", "instance", instance.ID)
        return
    }

    // Check resource availability
    if !d.resourceMgr.canAllocate(instance.InstanceType) {
        slog.Warn("Insufficient resources for restart", "instance", instance.ID)
        return
    }

    // Exponential backoff
    delay := policy.BackoffBase * time.Duration(1<<min(instance.CrashCount-1, 5))
    if delay > policy.BackoffMax {
        delay = policy.BackoffMax
    }
    slog.Info("Scheduling instance restart",
        "instance", instance.ID,
        "reason", reason,
        "delay", delay,
        "attempt", instance.CrashCount)

    time.AfterFunc(delay, func() {
        d.restartCrashedInstance(instance)
    })
}
```

#### B3: Restart Execution

```go
func (d *Daemon) restartCrashedInstance(instance *vm.VM) {
    // Re-verify instance should still restart (state may have changed during backoff)
    instance.Mu.Lock()
    if instance.Status != vm.StateError {
        instance.Mu.Unlock()
        return
    }
    instance.Mu.Unlock()

    slog.Info("Restarting crashed instance", "instance", instance.ID, "attempt", instance.CrashCount)

    // Transition: error → pending
    d.TransitionState(instance, vm.StatePending)

    // Relaunch (reuses existing LaunchInstance flow: mount volumes → start QEMU)
    err := d.LaunchInstance(instance)
    if err != nil {
        slog.Error("Failed to restart instance", "instance", instance.ID, "error", err)
        d.TransitionState(instance, vm.StateError)
        return
    }

    slog.Info("Instance restarted successfully", "instance", instance.ID)
}
```

### Phase C: Improved QMP Health Monitoring

The existing 30-second QMP heartbeat should detect more failure modes and trigger recovery.

#### C1: Enhanced QMP Heartbeat

Replace the current log-only heartbeat (daemon.go:1458-1490) with an actionable one:

```go
func (d *Daemon) qmpHealthCheck(instance *vm.VM) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    consecutiveFailures := 0
    const maxFailures = 3  // 90 seconds of QMP unresponsiveness

    for {
        select {
        case <-d.ctx.Done():
            return
        case <-ticker.C:
            // Skip terminal states
            instance.Mu.Lock()
            status := instance.Status
            instance.Mu.Unlock()
            if status == vm.StateStopping || status == vm.StateStopped ||
               status == vm.StateShuttingDown || status == vm.StateTerminated {
                return
            }

            resp, err := d.SendQMPCommand(instance.QMPClient,
                qmp.QMPCommand{Execute: "query-status"}, instance.ID)
            if err != nil {
                consecutiveFailures++
                slog.Warn("QMP health check failed",
                    "instance", instance.ID,
                    "consecutive_failures", consecutiveFailures,
                    "error", err)

                if consecutiveFailures >= maxFailures {
                    slog.Error("QMP unresponsive, checking process liveness",
                        "instance", instance.ID)

                    if !d.isInstanceProcessRunning(instance) {
                        // Process is dead — the exit watcher should handle this,
                        // but if it missed it (race), trigger crash handler
                        slog.Error("QEMU process dead, triggering crash recovery",
                            "instance", instance.ID)
                        d.handleInstanceCrash(instance, fmt.Errorf("qmp unresponsive, process dead"))
                        return
                    }
                    // Process alive but QMP hung — QEMU may be stuck
                    // Log and continue monitoring; operator can intervene
                    slog.Error("QEMU process alive but QMP unresponsive",
                        "instance", instance.ID)
                }
                continue
            }

            // Reset failure counter on success
            consecutiveFailures = 0
        }
    }
}
```

#### C2: Instance Health Status

Add health fields to the VM struct for observability:

```go
// In vm/vm.go, add to VM struct:
CrashCount      int       `json:"crash_count"`
LastCrashTime   time.Time `json:"last_crash_time,omitempty"`
LastCrashReason string    `json:"last_crash_reason,omitempty"`
HealthStatus    string    `json:"health_status"` // "healthy", "unhealthy", "recovering"
```

Expose via `hive get vms` and the EC2 DescribeInstances response:
```
INSTANCE            | STATUS  | HEALTH     | TYPE    | NODE  | CRASHES | LAST CRASH
i-abc123            | running | healthy    | t3.nano | node1 | 0       |
i-def456            | running | recovering | t3.nano | node2 | 2       | OOM 3m ago
i-ghi789            | error   | unhealthy  | t3.nano | node1 | 4       | exceeded max restarts
```

### Phase D: Host Resource Protection

Prevent OOM kills by enforcing resource limits at the OS level.

> **Dependency**: Phase D3 (memory pressure monitoring) feeds into Phase E's SystemStatus. When host memory pressure is high, the node's SystemStatus transitions to `impaired`.

#### D1: QEMU Process Memory Limits

When launching QEMU, set the process memory limit to slightly above the allocated VM memory. This ensures QEMU can't grow beyond its allocation and trigger a host-wide OOM.

```go
// In vm.go Config.Execute(), add to cmd.SysProcAttr:
func (c *Config) Execute() *exec.Cmd {
    cmd := exec.Command(c.QEMUPath, c.Args()...)

    // Set memory limit: VM memory + 256MB overhead (QEMU internal use)
    memLimitBytes := (c.MemoryMB + 256) * 1024 * 1024
    cmd.SysProcAttr = &syscall.SysProcAttr{
        // On Linux, use RLIMIT_AS or cgroups
    }

    return cmd
}
```

**Option A: cgroups v2 (recommended for production)**

Create a cgroup per QEMU process with a hard memory limit:

```go
func setCgroupMemoryLimit(pid int, limitBytes int64, instanceID string) error {
    cgroupPath := fmt.Sprintf("/sys/fs/cgroup/hive-vms/%s", instanceID)
    os.MkdirAll(cgroupPath, 0755)

    // Set memory limit
    os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.FormatInt(limitBytes, 10)), 0644)

    // Move process into cgroup
    os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0644)

    return nil
}
```

With cgroups, the OOM killer targets only the QEMU process in the cgroup — not random host processes. The `memory.max` is a hard limit; QEMU is killed cleanly if it exceeds it.

**Option B: OOM score adjustment (simpler, defense-in-depth)**

Protect system services by lowering their OOM score, and raise QEMU's score so VMs are killed before infrastructure:

```go
// Protect system services (predastore, viperblock, nats)
func protectSystemProcess(pid int) {
    os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), []byte("-500"), 0644)
}

// Make QEMU processes preferred OOM targets over system services
func setQEMUOOMScore(pid int) {
    os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), []byte("500"), 0644)
}
```

OOM score range: -1000 (never kill) to 1000 (kill first). Setting system services to -500 and QEMU to +500 ensures the OOM killer sacrifices VMs before infrastructure.

**Recommendation**: Implement Option B immediately (trivial, no dependencies). Add Option A (cgroups) as a follow-up for hard isolation.

#### D2: Service Resource Reservations

Reserve a portion of host resources for system services, reducing the amount available for VMs:

```go
// In NewResourceManager(), reserve resources for system services
const (
    systemReservedVCPU = 2       // 2 cores for nats/predastore/viperblock/daemon
    systemReservedMemGB = 2.0    // 2 GB for system services
)

func NewResourceManager(...) *ResourceManager {
    totalCPU := runtime.NumCPU()
    totalMem := getSystemMemoryGB()

    rm := &ResourceManager{
        availableVCPU: totalCPU - systemReservedVCPU,
        availableMem:  totalMem - systemReservedMemGB,
        // ...
    }
}
```

This is a soft limit (accounting only) but prevents the scheduler from over-committing the host. Hard enforcement comes from cgroups (D1).

#### D3: Memory Pressure Monitoring

Monitor host memory pressure and proactively stop accepting new VMs before OOM occurs:

```go
func (d *Daemon) monitorMemoryPressure() {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-d.ctx.Done():
            return
        case <-ticker.C:
            available := getAvailableMemoryGB()  // from /proc/meminfo MemAvailable
            total := getTotalMemoryGB()
            usedPercent := (1.0 - available/total) * 100

            if usedPercent > 90 {
                slog.Warn("Host memory pressure HIGH",
                    "used_percent", usedPercent,
                    "available_gb", available)
                // Unsubscribe from RunInstances topics to stop accepting new VMs
                // (re-subscribe when pressure drops below 80%)
            }
        }
    }
}
```

### Phase E: AWS-Compatible DescribeInstanceStatus API

Expose VM and host health through the standard AWS `DescribeInstanceStatus` API so clients (AWS CLI, SDKs, Terraform) get the same response format they expect from EC2.

> **Dependencies**:
> - Phases A-C provide the data (crash state, QMP health, process liveness)
> - Phase D provides host-level health (memory pressure, resource exhaustion)
> - [Phase 2.2 Node Modes](heartbeat-cluster-shutdown.md#phase-22--node-modes--health-monitoring-planned) provides `draining`/`maintenance` states that map to scheduled events

#### E1: AWS Response Format

The API returns two independent health dimensions per instance:

```json
{
    "InstanceStatuses": [
        {
            "InstanceId": "i-abc123",
            "AvailabilityZone": "ap-southeast-2a",
            "InstanceState": {
                "Code": 16,
                "Name": "running"
            },
            "SystemStatus": {
                "Status": "ok",
                "Details": [
                    {
                        "Name": "reachability",
                        "Status": "passed"
                    }
                ]
            },
            "InstanceStatus": {
                "Status": "ok",
                "Details": [
                    {
                        "Name": "reachability",
                        "Status": "passed"
                    }
                ]
            },
            "Events": []
        }
    ]
}
```

**SystemStatus** = host/infrastructure health (is the underlying node healthy?):

| Condition | SystemStatus | Detail Status | Source |
|-----------|-------------|---------------|--------|
| Node healthy, heartbeat current | `ok` | `passed` | Phase 2.1 heartbeat |
| Node heartbeat stale (>60s) | `impaired` | `failed` | Phase 2.2 `monitorPeerHeartbeats()` |
| Node in `draining` mode | `ok` (still operational) | `passed` | Phase 2.2 node modes |
| Node in `maintenance` mode | `impaired` | `failed` | Phase 2.2 node modes |
| Host memory pressure >90% | `impaired` | `failed` | Phase D3 memory pressure |
| Node unreachable (>120s no heartbeat) | `impaired` | `failed` | Phase 2.2 |

**InstanceStatus** = VM-level health (is this specific QEMU process healthy?):

| Condition | InstanceStatus | Detail Status | Source |
|-----------|---------------|---------------|--------|
| QEMU running, QMP responsive | `ok` | `passed` | Phase C QMP health |
| QMP unresponsive (1-2 failures) | `ok` | `passed` | Transient, not yet impaired |
| QMP unresponsive (3+ failures) | `impaired` | `failed` | Phase C consecutive failures |
| QEMU crashed, pending restart | `impaired` | `failed` | Phase A crash detection |
| QEMU crashed, restart failed | `impaired` | `failed` | Phase B restart exceeded |
| Instance stopped/terminated | `not-applicable` | `not-applicable` | Standard EC2 behavior |
| Instance just launched (<2 min) | `initializing` | `initializing` | Grace period |

**Events** — scheduled events map from [Phase 2.2 node modes](heartbeat-cluster-shutdown.md#phase-22--node-modes--health-monitoring-planned):

| Node Mode | Event Code | Description |
|-----------|------------|-------------|
| `draining` | `instance-retirement` | "The instance is scheduled for retirement" |
| `maintenance` | `system-maintenance` | "System maintenance is scheduled" |
| Instance recovering from crash | `instance-reboot` | "The instance is being restarted" |

```json
{
    "Events": [
        {
            "InstanceEventId": "evt-drain-node3-1707868800",
            "Code": "instance-retirement",
            "Description": "Node node3 is being drained for maintenance",
            "NotBefore": "2026-02-14T06:00:00+00:00"
        }
    ]
}
```

#### E2: Health State Tracking on VM Struct

Extend the VM struct to track both dimensions independently:

```go
// In vm/vm.go, add to VM struct:
type InstanceHealthState struct {
    // Instance-level (QEMU process health)
    InstanceStatus       string    `json:"instance_status"`        // "ok", "impaired", "initializing", "not-applicable"
    InstanceReachability string    `json:"instance_reachability"`  // "passed", "failed", "initializing"
    ImpairedSince        time.Time `json:"impaired_since,omitempty"`

    // Crash tracking (feeds into InstanceStatus)
    CrashCount      int       `json:"crash_count"`
    LastCrashTime   time.Time `json:"last_crash_time,omitempty"`
    LastCrashReason string    `json:"last_crash_reason,omitempty"`

    // QMP health (feeds into InstanceStatus)
    QMPConsecutiveFailures int  `json:"qmp_consecutive_failures"`
    LastQMPSuccess         time.Time `json:"last_qmp_success,omitempty"`
}
```

SystemStatus is not per-VM — it's derived from the node's state at response time:

```go
func (d *Daemon) buildSystemStatus() *ec2.InstanceStatusSummary {
    status := "ok"
    detailStatus := "passed"

    // Check node mode
    mode := d.nodeMode.Load().(string)
    if mode == "maintenance" {
        status = "impaired"
        detailStatus = "failed"
    }

    // Check memory pressure
    if d.memoryPressureHigh.Load() {
        status = "impaired"
        detailStatus = "failed"
    }

    return &ec2.InstanceStatusSummary{
        Status: aws.String(status),
        Details: []*ec2.InstanceStatusDetails{{
            Name:   aws.String("reachability"),
            Status: aws.String(detailStatus),
        }},
    }
}
```

InstanceStatus is per-VM, derived from the VM's health state:

```go
func (d *Daemon) buildInstanceStatus(instance *vm.VM) *ec2.InstanceStatusSummary {
    instance.Mu.Lock()
    health := instance.Health
    status := instance.Status
    launchTime := instance.Instance.LaunchTime
    instance.Mu.Unlock()

    // Not running → not-applicable
    if status != vm.StateRunning && status != vm.StateError {
        return &ec2.InstanceStatusSummary{
            Status: aws.String("not-applicable"),
            Details: []*ec2.InstanceStatusDetails{{
                Name:   aws.String("reachability"),
                Status: aws.String("not-applicable"),
            }},
        }
    }

    // Grace period: <2 minutes since launch → initializing
    if launchTime != nil && time.Since(*launchTime) < 2*time.Minute {
        return &ec2.InstanceStatusSummary{
            Status: aws.String("initializing"),
            Details: []*ec2.InstanceStatusDetails{{
                Name:   aws.String("reachability"),
                Status: aws.String("initializing"),
            }},
        }
    }

    // Crashed or QMP unresponsive → impaired
    if status == vm.StateError || health.QMPConsecutiveFailures >= 3 {
        return &ec2.InstanceStatusSummary{
            Status: aws.String("impaired"),
            Details: []*ec2.InstanceStatusDetails{{
                Name:           aws.String("reachability"),
                Status:         aws.String("failed"),
                ImpairedSince:  &health.ImpairedSince,
            }},
        }
    }

    return &ec2.InstanceStatusSummary{
        Status: aws.String("ok"),
        Details: []*ec2.InstanceStatusDetails{{
            Name:   aws.String("reachability"),
            Status: aws.String("passed"),
        }},
    }
}
```

#### E3: Gateway + NATS Implementation

Follow the exact pattern from `DescribeInstances`:

**Gateway handler** (`hive/gateway/ec2/instance/DescribeInstanceStatus.go`):
```go
func DescribeInstanceStatus(input *ec2.DescribeInstanceStatusInput, natsConn *nats.Conn, expectedNodes int) (*ec2.DescribeInstanceStatusOutput, error) {
    // 1. Marshal input to JSON
    // 2. Create inbox, subscribe
    // 3. PublishRequest to "ec2.DescribeInstanceStatus" (fan-out)
    // 4. Collect responses with 3s timeout
    // 5. Aggregate InstanceStatuses from all nodes
    // 6. Handle IncludeAllInstances flag (include non-running instances)
    // 7. Also query stopped instances via "ec2.DescribeStoppedInstanceStatus" (queue group)
    // 8. Return aggregated output
}
```

**Daemon handler** (`hive/daemon/daemon_handlers.go`):
```go
func (d *Daemon) handleEC2DescribeInstanceStatus(msg *nats.Msg) {
    var input ec2.DescribeInstanceStatusInput
    utils.UnmarshalJsonPayload(&input, msg.Data)

    systemStatus := d.buildSystemStatus()  // same for all instances on this node
    var statuses []*ec2.InstanceStatus

    d.Instances.Mu.Lock()
    for _, instance := range d.Instances.VMS {
        // Apply instance ID filter
        // Apply IncludeAllInstances (default: only running instances)

        instanceStatus := d.buildInstanceStatus(instance)
        events := d.buildInstanceEvents(instance)

        statuses = append(statuses, &ec2.InstanceStatus{
            InstanceId:       aws.String(instance.ID),
            AvailabilityZone: aws.String(d.config.AZ),
            InstanceState:    &ec2.InstanceState{
                Code: aws.Int64(vm.EC2StateCodes[instance.Status].Code),
                Name: aws.String(vm.EC2StateCodes[instance.Status].Name),
            },
            SystemStatus:   systemStatus,
            InstanceStatus: instanceStatus,
            Events:         events,
        })
    }
    d.Instances.Mu.Unlock()

    output := &ec2.DescribeInstanceStatusOutput{InstanceStatuses: statuses}
    jsonResponse, _ := json.Marshal(output)
    msg.Respond(jsonResponse)
}
```

**NATS subscription** (`hive/daemon/daemon.go` in `subscribeAll()`):
```go
{"ec2.DescribeInstanceStatus", d.handleEC2DescribeInstanceStatus, ""},
```

**Gateway route** (`hive/gateway/ec2.go` in `ec2Actions`):
```go
"DescribeInstanceStatus": ec2Handler(func(input *ec2.DescribeInstanceStatusInput, gw *GatewayConfig) (any, error) {
    return gateway_ec2_instance.DescribeInstanceStatus(input, gw.NATSConn, gw.DiscoverActiveNodes())
}),
```

#### E4: IncludeAllInstances Flag

Per AWS behavior:
- **Default** (`IncludeAllInstances=false`): Only return instances in `running` state
- **`IncludeAllInstances=true`**: Return all instances including stopped/terminated

For non-running instances, both SystemStatus and InstanceStatus are `not-applicable`.

#### E5: Cross-Node SystemStatus for Unreachable Nodes

When a node is unreachable, the gateway can still report status for instances that were last known on that node. The gateway queries stopped instance state from JetStream KV and builds a response with `SystemStatus: impaired`:

```go
// In the gateway, after fan-out collection:
// Check for nodes that didn't respond within timeout
// For instances on non-responding nodes (from KV state):
//   SystemStatus = "impaired" (host unreachable)
//   InstanceStatus = "impaired" (can't verify)
```

This requires the gateway to know which nodes own which instances — the JetStream KV `node.<name>` keys already track this.

---

## VM Struct Extensions

```go
// New fields on vm.VM
type VM struct {
    // ... existing fields ...

    // Health monitoring (Phase A-C)
    Health InstanceHealthState `json:"health"`
}

type InstanceHealthState struct {
    // Instance-level (QEMU process health)
    InstanceStatus       string    `json:"instance_status"`        // "ok", "impaired", "initializing", "not-applicable"
    InstanceReachability string    `json:"instance_reachability"`  // "passed", "failed", "initializing"
    ImpairedSince        time.Time `json:"impaired_since,omitempty"`

    // Crash tracking
    CrashCount      int       `json:"crash_count"`
    LastCrashTime   time.Time `json:"last_crash_time,omitempty"`
    LastCrashReason string    `json:"last_crash_reason,omitempty"`

    // QMP health
    QMPConsecutiveFailures int       `json:"qmp_consecutive_failures"`
    LastQMPSuccess         time.Time `json:"last_qmp_success,omitempty"`
}
```

---

## Files to Modify

| File | Phase | Action | Description |
|------|-------|--------|-------------|
| `hive/daemon/daemon.go` | A | Edit | Add `watchInstanceProcess()`, `handleInstanceCrash()`, call from `StartInstance()` and `reconnectInstance()` |
| `hive/daemon/daemon.go` | B | Edit | Add `maybeRestartInstance()`, `restartCrashedInstance()` |
| `hive/daemon/daemon.go` | C | Edit | Replace QMP heartbeat goroutine with `qmpHealthCheck()` |
| `hive/daemon/daemon.go` | D | Edit | Add system resource reservation to `NewResourceManager()` |
| `hive/daemon/daemon.go` | E | Edit | Add `ec2.DescribeInstanceStatus` to `subscribeAll()` |
| `hive/daemon/health.go` | A-D | New | `classifyCrashReason()`, `checkDmesgOOM()`, `RestartPolicy`, memory pressure monitor, `buildSystemStatus()`, `buildInstanceStatus()` |
| `hive/daemon/daemon_handlers.go` | E | Edit | Add `handleEC2DescribeInstanceStatus()`, include health in DescribeInstances |
| `hive/vm/vm.go` | A | Edit | Add `InstanceHealthState` struct and `Health` field to VM |
| `hive/vm/vm.go` | D | Edit | OOM score adjustment in `Config.Execute()` |
| `hive/utils/utils.go` | D | Edit | Add `protectSystemProcess()` for OOM score on services |
| `hive/gateway/ec2/instance/DescribeInstanceStatus.go` | E | New | Gateway handler: fan-out, aggregate, XML response |
| `hive/gateway/ec2.go` | E | Edit | Add `DescribeInstanceStatus` to `ec2Actions` map |
| `cmd/hive/cmd/get.go` | C | Edit | Show health status in `hive get vms` output |
| `hive/daemon/health_test.go` | A-C | New | Tests for crash classification, restart policy, backoff |
| `hive/gateway/ec2/instance/DescribeInstanceStatus_test.go` | E | New | Gateway handler tests, multi-node aggregation |

---

## Testing Strategy

### Unit Tests

- `TestCrashReasonClassification` — SIGKILL → "oom-killed", SIGSEGV → "segfault", exit code 1 → "exit-1"
- `TestRestartPolicyMaxRestarts` — verify restart is denied after max restarts in window
- `TestRestartPolicyBackoff` — verify exponential backoff: 5s, 10s, 20s, 40s, 80s, 120s (capped)
- `TestRestartPolicyDrainingNode` — no restart when node mode != normal
- `TestResourceDeallocationOnCrash` — verify phantom reservations are freed
- `TestQMPHealthCheckConsecutiveFailures` — 3 failures triggers liveness check
- `TestBuildSystemStatus` — node mode → SystemStatus mapping
- `TestBuildInstanceStatus` — QMP failures/crash state → InstanceStatus mapping
- `TestDescribeInstanceStatusHandler` — NATS handler returns correct format
- `TestDescribeInstanceStatusGateway` — fan-out aggregation, XML response

### Integration Tests (Docker E2E)

**Test: QEMU crash detection and restart**

```bash
# 1. Launch a VM
aws ec2 run-instances --instance-type t3.nano ...

# 2. Verify running and healthy via describe-instance-status
aws ec2 describe-instance-status --instance-ids $INSTANCE_ID
# SystemStatus: ok, InstanceStatus: ok (or initializing if <2min)

# 3. Kill QEMU process (simulates crash)
# Inside Docker: kill -9 $(cat ~/node1/run/i-<id>.pid)

# 4. Wait 5-10 seconds

# 5. Verify describe-instance-status shows impaired then recovering
aws ec2 describe-instance-status --instance-ids $INSTANCE_ID --include-all-instances
# InstanceStatus: impaired (during crash), then ok (after restart)

# 6. Verify auto-restart — instance is running again
aws ec2 describe-instances  # state: running

# 7. Verify SSH still works (new QEMU process, same volumes)
ssh -p <port> ec2-user@127.0.0.1
```

**Test: DescribeInstanceStatus AWS CLI compatibility**

```bash
# 1. Launch 3 VMs across nodes
# 2. describe-instance-status with no args (running only)
aws ec2 describe-instance-status
# Returns 3 instances, all SystemStatus: ok, InstanceStatus: ok

# 3. Stop one instance
aws ec2 stop-instances --instance-ids $INSTANCE_ID

# 4. describe-instance-status (default: running only)
aws ec2 describe-instance-status
# Returns 2 instances (stopped one excluded)

# 5. describe-instance-status --include-all-instances
aws ec2 describe-instance-status --include-all-instances
# Returns 3 instances, stopped one has status: not-applicable
```

**Test: OOM score protection**

```bash
# 1. Check OOM scores
cat /proc/$(cat ~/node1/run/nats.pid)/oom_score_adj     # should be -500
cat /proc/$(cat ~/node1/run/predastore.pid)/oom_score_adj # should be -500
cat /proc/<qemu-pid>/oom_score_adj                        # should be +500
```

**Test: Restart loop prevention**

```bash
# 1. Launch VM
# 2. Kill QEMU 4 times rapidly (exceeds max restarts of 3 in 10 min)
# 3. Verify instance stays in "error" state after 3rd crash
# 4. describe-instance-status shows InstanceStatus: impaired persistently
# 5. Verify hive get vms shows "unhealthy" with crash count
```

### Remote Cluster Tests

**Test: Real OOM kill scenario**

```bash
# 1. Launch a VM with most of the host memory
# 2. SSH into VM, run: stress-ng --vm 1 --vm-bytes 95% --timeout 120s
# 3. Alternatively, from host: run a memory-hungry process to trigger OOM
# 4. Verify QEMU gets OOM-killed (check dmesg)
# 5. Verify daemon detects crash, logs reason as "oom-killed"
# 6. describe-instance-status shows InstanceStatus: impaired, SystemStatus: impaired (memory pressure)
# 7. Verify auto-restart with backoff
# 8. Verify system services (predastore, viperblock) survived (protected by OOM score)
```

**Test: Resource pressure response**

```bash
# 1. Launch VMs until host is at 85% memory
# 2. Verify hive top shows accurate allocation
# 3. Try to launch another VM — should succeed if capacity allows
# 4. Push to 90% — verify scheduler stops accepting new VMs
# 5. describe-instance-status shows SystemStatus: impaired for all instances on that node
# 6. Terminate a VM — verify scheduler resumes accepting, SystemStatus recovers to ok
```

---

## Implementation Priority

```
Phase A (Crash Detection)     ✅ DONE — detects QEMU crashes at runtime
  │
  ├── A1: Exit watcher goroutine (startupConfirmed channel in StartInstance)
  ├── A2: Crash handler + resource deallocation (handleInstanceCrash)
  └── A3: Crash reason classification (classifyCrashReason)
  │
Phase B (Auto-Restart)        ✅ DONE — exponential backoff, restart limits
  │
  ├── B1: Restart policy (maxRestartsInWindow, restartWindow constants)
  ├── B2: Restart decision with backoff (maybeRestartInstance)
  └── B3: Restart execution (restartCrashedInstance via LaunchInstance)
  │
Phase C (QMP Improvements)    ← defense-in-depth
  │
  ├── C1: Enhanced QMP heartbeat
  └── C2: Health status fields on VM struct
  │
Phase D (Resource Protection)  ← prevents the problem at the source
  │
  ├── D1: OOM score adjustment ✅ DONE (QEMU +500, daemon -500, dev services -500)
  ├── D1b: cgroups per QEMU (follow-up)
  ├── D2: System resource reservation
  └── D3: Memory pressure monitoring
  │
Phase E (AWS API)             ← exposes all the above via standard EC2 API
  │
  ├── E1: DescribeInstanceStatus gateway handler
  ├── E2: SystemStatus + InstanceStatus builders in daemon
  ├── E3: NATS handler + subscription
  ├── E4: IncludeAllInstances flag
  └── E5: Cross-node status for unreachable nodes
```

**Recommended order**: A → B → D1 → C → E → D2 → D3 → D1b

Phases A and B are the core — detect crashes and restart VMs. D1 (OOM scores) is trivial and prevents the worst-case scenario. C adds the health state fields that Phase E consumes. E (DescribeInstanceStatus API) is the externally visible surface — once A-C provide the data, E is straightforward plumbing (follows the exact DescribeInstances pattern). D2-D3 are operational polish. D1b (cgroups) is the hardest and least urgent since OOM scores provide 80% of the protection.

---

## Key Design Decisions

1. **Exit watcher vs polling**: Use `cmd.Wait()` (already exists) rather than periodic PID polling. It's event-driven, zero overhead, and catches the exact moment of exit. The QMP heartbeat is a secondary check for edge cases where the exit channel is missed.

2. **Restart on same node**: Crashed VMs restart on the same node by default. Cross-node migration is Phase 2.7's concern. If the crash was OOM and resources are tight, the restart policy can optionally defer to the scheduler (future work).

3. **State file not needed for crash restart**: Unlike cold migration (Phase 2.7a), a crash restart doesn't preserve VM memory — the VM boots fresh from its root volume. This is equivalent to a hard power cycle, which is the correct behavior for an unexpected crash.

4. **OOM scores over cgroups**: OOM score adjustment is 2 lines of code and works everywhere. Cgroups require root, cgroup v2 availability, and careful setup. Start with OOM scores.

5. **No restart for terminated instances**: If the user explicitly terminated the VM, don't restart it. The exit watcher checks the VM state before triggering recovery.

---

## Cross-References & Dependencies

This plan and [heartbeat-cluster-shutdown.md](heartbeat-cluster-shutdown.md) are tightly coupled. Here's the dependency map:

### What this plan depends on from heartbeat-cluster-shutdown.md

| Dependency | Phase | Why |
|------------|-------|-----|
| Heartbeats (Phase 2.1) | **Complete** | SystemStatus uses heartbeat staleness to detect unreachable nodes |
| Node Modes (Phase 2.2) | **Planned** | `draining`/`maintenance` modes map to SystemStatus `impaired` and scheduled Events |
| Node Modes (Phase 2.2) | **Planned** | `nodeMode` check in restart policy — don't restart VMs on draining nodes |
| VM Migration (Phase 2.7) | **Planned** | When auto-restart fails (OOM, no resources), cross-node migration is the fallback |

### What heartbeat-cluster-shutdown.md depends on from this plan

| Dependency | Phase | Why |
|------------|-------|-----|
| Crash Detection (Phase A) | Core | Phase 2.7 (VM Migration) needs to detect when a VM dies on a failing node to trigger migration |
| Health State (Phase C2) | Core | Phase 2.2 `monitorPeerHeartbeats()` can use QMP health data to decide if a node is "suspect" |
| DescribeInstanceStatus (Phase E) | API | Phase 2.2 node drain should update SystemStatus for all VMs on the draining node |
| Resource Protection (Phase D) | Operational | Phase 2.6 (Degraded Writes) benefits from memory pressure signals to throttle writes |

### Implementation Order (Combined)

```
heartbeat-cluster-shutdown.md          ec2-health-restart.md
─────────────────────────────          ─────────────────────
Phase 2.1 Heartbeats ✅
Phase 2.4 Shutdown ✅
                                       Phase A: Crash Detection ✅
                                       Phase B: Auto-Restart ✅
                                       Phase D1: OOM Scores ✅
Phase 2.2 Node Modes ◄────────────────Phase C: QMP Health + Status Fields
                                       Phase E: DescribeInstanceStatus API
Phase 2.7a Cold Migration ◄───────────(uses crash detection)
Phase 2.5 Rolling Upgrade
Phase 2.6 Degraded Writes ◄───────────(uses memory pressure signals)
                                       Phase D2-D3: Resource Reservation
Phase 2.7b Live Migration
                                       Phase D1b: cgroups
```

Phases A, B, D1 from this plan should be implemented **before** Phase 2.2 from heartbeat-cluster-shutdown.md. Phase C and E should be implemented **alongside** Phase 2.2 since they share the health state infrastructure.

---

## Files Modified (Phases A, B, D1)

| File | Action | Description |
|------|--------|-------------|
| `hive/vm/vm.go` | Edit | Added `InstanceHealthState` struct and `Health` field to VM |
| `hive/vm/state.go` | Edit | Added `StatePending` to `StateError` valid transitions (Error→Pending for restart) |
| `hive/utils/utils.go` | Edit | Added `SetOOMScore()` helper (Linux-only, writes to `/proc/<pid>/oom_score_adj`) |
| `hive/daemon/health.go` | New | `classifyCrashReason()`, `handleInstanceCrash()`, `unmountInstanceVolumes()`, `maybeRestartInstance()`, `restartCrashedInstance()` |
| `hive/daemon/daemon.go` | Edit | Added `startupConfirmed` channel to QEMU launch goroutine for crash watcher, QEMU OOM score (+500) after `cmd.Start()`, daemon self-protection OOM score (-500) in `Start()` |
| `scripts/start-dev.sh` | Edit | Added `set_oom_score()` helper, set -500 for all infrastructure services |
| `hive/daemon/health_test.go` | New | Tests: crash classification (OOM, exit code, clean, unknown), skip guards, restart limits, window reset, exponential backoff |
| `hive/utils/utils_test.go` | Edit | Added `TestSetOOMScore` and `TestSetOOMScore_InvalidPID` |
| `docs/development/feature/ec2-health-restart.md` | Edit | Updated status, marked A/B/D1 complete |
