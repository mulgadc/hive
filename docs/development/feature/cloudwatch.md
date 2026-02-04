# CloudWatch Service

## Overview

MulgaOS Hive CloudWatch provides AWS-compatible monitoring and observability for EC2 instances, EBS volumes, and other infrastructure components. The service collects, stores, and exposes metrics that can be queried via the AWS CloudWatch API.

## Core Features

### 1. Metric Collection

- **EC2 Metrics**: CPU utilization, network I/O, disk I/O, status checks
- **EBS Metrics**: Volume read/write ops, throughput, latency, queue length
- **System Metrics**: Node health, service status, resource utilization

### 2. Metric Storage

Using NATS JetStream for time-series data:

```go
type Metric struct {
    Namespace   string
    MetricName  string
    Dimensions  []Dimension
    Timestamp   time.Time
    Value       float64
    Unit        string
    AccountID   string
}

type Dimension struct {
    Name  string
    Value string
}
```

### 3. API Operations

| Operation | Status | Description |
|-----------|--------|-------------|
| PutMetricData | ❌ TODO | Publish custom metrics |
| GetMetricData | ❌ TODO | Query metrics with math expressions |
| GetMetricStatistics | ❌ TODO | Query metrics with aggregation |
| ListMetrics | ❌ TODO | List available metrics |
| PutMetricAlarm | ❌ TODO | Create alarms |
| DescribeAlarms | ❌ TODO | List alarms |
| DeleteAlarms | ❌ TODO | Remove alarms |
| SetAlarmState | ❌ TODO | Manual alarm state change |

## Architecture

### Hybrid Storage Strategy

CloudWatch in MulgaOS uses a **hybrid storage approach** to optimize for both performance and scale:

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   EC2 Daemon    │───>│  CloudWatch     │───>│   NATS KV       │
│ (metric source) │    │    Service      │    │ (hot metrics)   │
└─────────────────┘    └─────────────────┘    └─────────────────┘
       │                      │                       │
       │                      │                       │ (TTL roll-off)
       ▼                      ▼                       ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│  QEMU Guest     │    │  Alarm Engine   │    │   Predastore    │
│     Agent       │    │  (evaluator)    │    │ (cold metrics)  │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

### Storage Tiers

| Tier | Storage | Retention | Use Case |
|------|---------|-----------|----------|
| Hot | NATS KV | 1-24 hours | Real-time dashboards, active alarms |
| Warm | NATS KV (archived) | 1-7 days | Recent queries, trending |
| Cold | Predastore (S3) | 14-365+ days | Historical analysis, compliance |

### Why Hybrid Storage?

1. **NATS KV** is optimized for:
   - Low-latency reads (sub-millisecond)
   - High-frequency updates (thousands/second)
   - Real-time alarm evaluation
   - Active metric aggregation

2. **Predastore (S3)** is optimized for:
   - Large-volume storage (petabytes)
   - Cost-effective long-term retention
   - Batch analytics and historical queries
   - Log storage (CloudWatch Logs)

### Metric Roll-off Strategy

```go
// Roll-off configuration
type MetricRolloffConfig struct {
    hot_retention_hours  int    // NATS KV hot tier (default: 24)
    warm_retention_days  int    // NATS KV warm tier (default: 7)
    cold_retention_days  int    // S3 cold tier (default: 365)
    aggregation_interval string // Aggregation before S3 (default: "5m")
}
```

Metrics flow through tiers:
1. **Ingest**: New metrics → NATS KV (1-minute resolution)
2. **Aggregate**: After 24h, aggregate to 5-minute resolution
3. **Archive**: After 7 days, move to S3 with daily aggregation
4. **Purge**: After retention period, delete from S3

## QEMU Guest Agent Integration

For enhanced monitoring, MulgaOS integrates with QEMU Guest Agent (QGA) to collect in-guest metrics.

### Enabling QEMU Guest Agent

**Option 1: Cloud-init bootstrap**

Add to AMI cloud-init user-data:
```yaml
#cloud-config
package_update: true
packages:
  - qemu-guest-agent
runcmd:
  - systemctl enable qemu-guest-agent
  - systemctl start qemu-guest-agent
```

**Option 2: Pre-installed in AMI**

For official MulgaOS AMIs, qemu-guest-agent is pre-installed and enabled.

### QGA-Enabled Metrics

| Metric | Source | Description |
|--------|--------|-------------|
| guest-sync | QGA | Agent connectivity check |
| guest-get-host-name | QGA | Guest hostname |
| guest-get-time | QGA | Guest clock time |
| guest-get-vcpus | QGA | vCPU information |
| guest-get-memory-blocks | QGA | Memory block info |
| guest-get-disks | QGA | Disk information |
| guest-get-fsinfo | QGA | Filesystem usage |
| guest-get-network-interfaces | QGA | Network interface stats |

### Implementation

```go
// Query QEMU Guest Agent for filesystem info
func (d *Daemon) getGuestFilesystemInfo(instanceID string) ([]FSInfo, error) {
    qmpClient := d.qmpClients[instanceID]
    if qmpClient == nil {
        return nil, errors.New("QMP client not available")
    }

    // Execute QGA command via QMP
    response, err := qmpClient.Execute("guest-get-fsinfo", nil)
    if err != nil {
        return nil, fmt.Errorf("QGA command failed: %w", err)
    }

    var fsInfo []FSInfo
    json.Unmarshal(response, &fsInfo)
    return fsInfo, nil
}
```

### QGA Communication Path

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│   Hive Daemon    │────>│   QMP Socket     │────>│  QEMU Process    │
│                  │     │ (qmp-i-xxx.sock) │     │                  │
└──────────────────┘     └──────────────────┘     └──────────────────┘
                                                          │
                                                          ▼
                                                  ┌──────────────────┐
                                                  │  Guest Agent     │
                                                  │  (qemu-ga)       │
                                                  └──────────────────┘
```

## Amazon CloudWatch Agent Integration

MulgaOS supports the **Amazon CloudWatch Agent** (MIT License, written in Go) for extended monitoring.

### Why Use CloudWatch Agent?

1. **Drop-in AWS compatibility** - Uses same configuration format
2. **Extended metrics** - Memory, disk, process-level stats
3. **Log collection** - Push logs to CloudWatch Logs
4. **StatsD/collectd support** - Aggregate custom metrics

### Installation

```bash
# Inside EC2 instance
wget https://s3.amazonaws.com/amazoncloudwatch-agent/debian/amd64/latest/amazon-cloudwatch-agent.deb
dpkg -i amazon-cloudwatch-agent.deb
```

### Configuration for MulgaOS

Create `/opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json`:

```json
{
  "agent": {
    "metrics_collection_interval": 60,
    "run_as_user": "cwagent"
  },
  "metrics": {
    "namespace": "CWAgent",
    "append_dimensions": {
      "InstanceId": "${aws:InstanceId}"
    },
    "metrics_collected": {
      "mem": {
        "measurement": ["mem_used_percent"]
      },
      "disk": {
        "measurement": ["disk_used_percent"],
        "resources": ["/", "/data"]
      }
    }
  }
}
```

### MulgaOS Endpoint Configuration

```bash
# Configure agent to use MulgaOS endpoints
export AWS_EC2_METADATA_SERVICE_ENDPOINT="http://169.254.169.254"
# Agent will automatically use metadata service for credentials and endpoint discovery
```

## Log Storage with Predastore

CloudWatch Logs are stored in Predastore (S3) for cost-effective retention.

### Log Group Structure

```
predastore://cloudwatch-logs/
  ├── {account_id}/
  │   ├── {log_group_name}/
  │   │   ├── {log_stream_name}/
  │   │   │   ├── 2026/02/01/00/chunk-0001.log.gz
  │   │   │   ├── 2026/02/01/00/chunk-0002.log.gz
  │   │   │   └── ...
```

### Log Ingestion Pipeline

```
┌────────────────┐    ┌────────────────┐    ┌────────────────┐
│  Log Events    │───>│  NATS Stream   │───>│  Log Compactor │
│  (PutLogEvents)│    │  (buffer)      │    │  (goroutine)   │
└────────────────┘    └────────────────┘    └────────────────┘
                                                    │
                                                    ▼
                                            ┌────────────────┐
                                            │   Predastore   │
                                            │  (S3 storage)  │
                                            └────────────────┘
```

### Configuration

```toml
[cloudwatch.logs]
enabled = true
buffer_size = 10000           # Events buffered before flush
flush_interval = "30s"        # Max time before flush
compression = "gzip"          # Compression for S3
retention_days = 30           # Default log retention
max_event_size = 262144       # 256KB max per event
```

## Implementation Plan

### Phase 1: Gateway & Handler Structure ✅ READY

1. Create `hive/gateway/cloudwatch/` directory structure
2. Implement CloudWatch request parser (XML/JSON)
3. Create `hive/handlers/cloudwatch/` service interfaces
4. Set up NATS subscriptions for `cloudwatch.*` subjects

### Phase 2: Metric Collection & Storage

1. Define metric collection interfaces
2. Implement NATS KV-based metric store (hot tier)
3. Add metric collectors for EC2 instances (via QMP)
4. Build time-series query engine
5. Implement Predastore archival (cold tier)

### Phase 3: API Implementation (Core)

1. Implement `PutMetricData` handler
2. Implement `GetMetricData` handler
3. Implement `GetMetricStatistics` handler
4. Implement `ListMetrics` handler
5. Implement `ListTagsForResource` / `TagResource` / `UntagResource`

### Phase 4: Alarms

1. Define alarm data model in NATS KV
2. Implement alarm evaluation goroutine
3. Implement `PutMetricAlarm` / `DeleteAlarms` handlers
4. Implement `DescribeAlarms` / `DescribeAlarmsForMetric`
5. Implement `SetAlarmState` / `EnableAlarmActions` / `DisableAlarmActions`
6. Add NATS-based alarm notifications (future SNS integration)

### Phase 5: Dashboards & Insights

1. Implement `PutDashboard` / `GetDashboard` / `DeleteDashboards`
2. Implement `ListDashboards`
3. Implement `GetMetricWidgetImage` (future)

### Phase 6: CloudWatch Logs

1. Implement log group/stream management
2. Implement `PutLogEvents` with NATS buffering
3. Implement log compaction to Predastore
4. Implement `GetLogEvents` / `FilterLogEvents`

### Phase 7: Advanced Features (Future)

1. Anomaly detection (`PutAnomalyDetector`)
2. Insight rules (`PutInsightRule`)
3. Metric streams (`PutMetricStream`)
4. Composite alarms (`PutCompositeAlarm`)

### Phase 8: QEMU Guest Agent Integration

1. Add QGA communication via QMP socket
2. Implement guest filesystem metrics collection
3. Implement guest network interface monitoring
4. Add memory/disk utilization from guest perspective

## Configuration

### hive.toml

```toml
[cloudwatch]
enabled = true
retention_days = 14
collection_interval = "1m"
max_metrics_per_request = 1000

[cloudwatch.storage]
bucket = "hive-cloudwatch-metrics"
```

## Standard EC2 Metrics

### Instance Metrics

| Metric | Unit | Description |
|--------|------|-------------|
| CPUUtilization | Percent | CPU usage percentage |
| DiskReadOps | Count | Disk read operations |
| DiskWriteOps | Count | Disk write operations |
| DiskReadBytes | Bytes | Disk read throughput |
| DiskWriteBytes | Bytes | Disk write throughput |
| NetworkIn | Bytes | Network ingress |
| NetworkOut | Bytes | Network egress |
| NetworkPacketsIn | Count | Network packets received |
| NetworkPacketsOut | Count | Network packets sent |
| StatusCheckFailed | Count | Combined status check |
| StatusCheckFailed_Instance | Count | Instance check failed |
| StatusCheckFailed_System | Count | System check failed |

### EBS Metrics

| Metric | Unit | Description |
|--------|------|-------------|
| VolumeReadOps | Count | Read operations |
| VolumeWriteOps | Count | Write operations |
| VolumeReadBytes | Bytes | Read throughput |
| VolumeWriteBytes | Bytes | Write throughput |
| VolumeTotalReadTime | Seconds | Total read time |
| VolumeTotalWriteTime | Seconds | Total write time |
| VolumeIdleTime | Seconds | Idle time |
| VolumeQueueLength | Count | I/O queue depth |
| BurstBalance | Percent | Burst credit balance |

## CloudWatch API Operations

Full list of CloudWatch operations to implement (from `aws cloudwatch help`):

### Alarms
- [ ] `delete-alarms` - Delete specified alarms
- [ ] `describe-alarm-history` - Get alarm state history
- [ ] `describe-alarms` - List alarms by state/prefix
- [ ] `describe-alarms-for-metric` - Get alarms for specific metric
- [ ] `disable-alarm-actions` - Disable actions for alarms
- [ ] `enable-alarm-actions` - Enable actions for alarms
- [ ] `put-composite-alarm` - Create composite alarm
- [ ] `put-metric-alarm` - Create/update metric alarm
- [ ] `set-alarm-state` - Manually set alarm state

### Anomaly Detection
- [ ] `delete-anomaly-detector` - Delete anomaly detector
- [ ] `describe-anomaly-detectors` - List anomaly detectors
- [ ] `put-anomaly-detector` - Create/update anomaly detector

### Dashboards
- [ ] `delete-dashboards` - Delete specified dashboards
- [ ] `get-dashboard` - Get dashboard body
- [ ] `list-dashboards` - List available dashboards
- [ ] `put-dashboard` - Create/update dashboard

### Insight Rules
- [ ] `delete-insight-rules` - Delete insight rules
- [ ] `describe-alarm-contributors` - Get top contributors
- [ ] `describe-insight-rules` - List insight rules
- [ ] `disable-insight-rules` - Disable insight rules
- [ ] `enable-insight-rules` - Enable insight rules
- [ ] `get-insight-rule-report` - Get insight rule results
- [ ] `list-managed-insight-rules` - List managed rules
- [ ] `put-insight-rule` - Create insight rule
- [ ] `put-managed-insight-rules` - Create managed rules

### Metrics
- [ ] `get-metric-data` - Query metrics with expressions
- [ ] `get-metric-statistics` - Query with aggregation
- [ ] `get-metric-widget-image` - Get PNG of metric graph
- [ ] `list-metrics` - List available metrics
- [ ] `put-metric-data` - Publish custom metrics

### Metric Streams
- [ ] `delete-metric-stream` - Delete metric stream
- [ ] `get-metric-stream` - Get stream configuration
- [ ] `list-metric-streams` - List metric streams
- [ ] `put-metric-stream` - Create/update metric stream
- [ ] `start-metric-streams` - Start streaming metrics
- [ ] `stop-metric-streams` - Stop streaming metrics

### Snapshot Access (Public Access Control)
- [ ] `disable-snapshot-block-public-access` - Disable public access
- [ ] `enable-snapshot-block-public-access` - Enable public access
- [ ] `get-snapshot-block-public-access-state` - Get current state

### Tags
- [ ] `list-tags-for-resource` - List tags on CloudWatch resource
- [ ] `tag-resource` - Add tags to resource
- [ ] `untag-resource` - Remove tags from resource

### Utility
- [ ] `wait` - Wait for alarm state

## Dependencies

- NATS JetStream for metric storage
- Predastore (S3) for log storage and cold metrics
- Health Check Service for status metrics
- EC2 Daemon for instance metrics
- Viperblock for EBS metrics
- QEMU Guest Agent for in-guest metrics (optional)

## References

- [AWS CloudWatch Metrics Reference](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/aws-services-cloudwatch-metrics.html)
- [CloudWatch API Reference](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/Welcome.html)

---

*Last Updated: February 2026*
