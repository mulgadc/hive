# EC2 Instance Health Checks

## Overview

MulgaOS Hive implements EC2-compatible instance health checks to detect hardware, software, and connectivity issues that could prevent applications from running correctly. This feature provides automated status checks running every minute on all running instances.

## Status Check Categories

### 1. System Status Checks

System status checks detect problems with the underlying infrastructure hosting the instance:

- **Power loss** - Physical server power failure
- **Host hardware failure** - CPU, memory, or other hardware issues
- **Network connectivity issues** - Network path problems to the host
- **Storage subsystem issues** - Viperblock service unavailability

**Implementation**: Monitor the physical node health via NATS heartbeats and Viperblock status.

### 2. Instance Status Checks

Instance status checks focus on the health of the guest operating system:

- **Network stack** - ARP/ICMP checks to verify network connectivity
- **Boot issues** - Kernel panic, stuck boot process
- **Resource exhaustion** - Memory exhaustion, disk full
- **Filesystem corruption** - Read-only filesystem detection

**Implementation**: Use QEMU QMP socket to monitor guest agent and network responses.

### 3. Attached EBS Status Checks

Monitor whether all EBS volumes attached to an instance are reachable:

- **I/O operations** - Ability to complete read/write operations
- **Storage path** - Connectivity between instance and volumes
- **Volume health** - Viperblock volume status and error logs

**Implementation**: Query Viperblock for volume status and monitor NBD connection health.

## Implementation Plan

### Phase 1: Infrastructure (Week 1-2)

1. **Health Check Service** (`hive/daemon/health.go`)
   ```go
   type HealthCheckService struct {
       daemon     *Daemon
       interval   time.Duration
       checks     map[string]*InstanceHealthStatus
       mu         sync.RWMutex
   }

   type InstanceHealthStatus struct {
       InstanceID           string
       SystemStatus         StatusCheckResult
       InstanceStatus       StatusCheckResult
       EBSStatus            StatusCheckResult
       LastChecked          time.Time
       ImpairedSince        *time.Time
   }

   type StatusCheckResult struct {
       Status    string    // "ok", "impaired", "insufficient-data"
       Details   string
       Timestamp time.Time
   }
   ```

2. **NATS Health Topics**
   - `hive.health.status` - Publish health check results
   - `hive.health.events` - Publish impairment events
   - `hive.health.metrics` - Expose metrics for CloudWatch

### Phase 2: System Checks (Week 2-3)

1. **Node Health Monitoring**
   - NATS heartbeat monitoring between nodes
   - Detect node failures (timeout after 30s)
   - Track last successful heartbeat per node

2. **Hardware Monitoring**
   - CPU temperature/throttling via `/sys/class/thermal`
   - Memory availability via `/proc/meminfo`
   - Disk health via SMART data

3. **Viperblock Integration**
   - Monitor Viperblock service health
   - Track volume error rates
   - Detect NBD disconnections

### Phase 3: Instance Checks (Week 3-4)

1. **QEMU QMP Integration**
   - Query guest agent status (`guest-ping`)
   - Monitor VNC/serial console availability
   - Track QEMU process health

2. **Network Checks**
   - ARP resolution to instance IP
   - ICMP ping to instance (if enabled)
   - OVS port status monitoring

3. **Resource Monitoring**
   - Track instance CPU/memory from host perspective
   - Monitor disk I/O rates
   - Detect runaway processes

### Phase 4: EBS Checks (Week 4-5)

1. **Volume Status**
   - Poll Viperblock for volume status
   - Monitor NBD connection state
   - Track I/O latency and errors

2. **Attachment Health**
   - Verify volume attachment consistency
   - Detect stale attachments
   - Monitor multipath status (if applicable)

## API Implementation

### DescribeInstanceStatus

```go
func (s *InstanceServiceImpl) DescribeInstanceStatus(ctx context.Context, input *ec2.DescribeInstanceStatusInput) (*ec2.DescribeInstanceStatusOutput, error) {
    // Get health status from HealthCheckService
    // Filter by InstanceIds if provided
    // Return formatted status checks
}
```

### ReportInstanceStatus

```go
func (s *InstanceServiceImpl) ReportInstanceStatus(ctx context.Context, input *ec2.ReportInstanceStatusInput) (*ec2.ReportInstanceStatusOutput, error) {
    // Record user-reported status
    // Useful for manual intervention tracking
}
```

## CloudWatch Integration

Health check failures should increment CloudWatch metrics:

- `StatusCheckFailed_System` - System status check failed
- `StatusCheckFailed_Instance` - Instance status check failed
- `StatusCheckFailed_AttachedEBS` - EBS status check failed
- `StatusCheckFailed` - Any status check failed (composite)

## Recovery Actions

### Automatic Recovery

For instances with appropriate settings:

1. **EBS-backed instances**: Stop and start on new host
2. **Instance store instances**: Reboot attempt, then terminate if failed

### Manual Intervention Required

- Filesystem corruption
- Kernel panics
- Network misconfigurations

## Configuration

### hive.toml

```toml
[health]
enabled = true
check_interval = "1m"
system_check_enabled = true
instance_check_enabled = true
ebs_check_enabled = true
recovery_enabled = false  # Manual approval required by default

[health.thresholds]
network_timeout = "5s"
qmp_timeout = "10s"
ebs_timeout = "30s"
consecutive_failures = 2  # Mark impaired after N failures
```

## Testing

### Unit Tests (`health_test.go`)

1. Test each check type independently
2. Mock QEMU QMP responses
3. Mock Viperblock status
4. Verify state transitions

### Integration Tests (`health_e2e_test.go`)

1. Launch instance and verify initial "ok" status
2. Simulate failures (kill QEMU, disconnect NBD)
3. Verify status transitions to "impaired"
4. Test recovery detection

## Dependencies

- QEMU QMP socket access (existing)
- Viperblock service communication (existing)
- NATS JetStream for status persistence (existing)
- OVS monitoring for network checks (existing)

## References

- [AWS EC2 Status Checks](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/monitoring-system-instance-status-check.html)
- [CloudWatch Metrics for Status Checks](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html)

---

*Last Updated: February 2026*
