# Hive Architecture

Hive is an AWS-compatible infrastructure platform for bare-metal, edge, and on-premise deployments. It provides EC2-compatible VM orchestration backed by QEMU/KVM, with storage provided by companion projects Viperblock (EBS) and Predastore (S3).

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              AWS SDK / CLI                                  │
│                    (aws ec2 run-instances --endpoint-url ...)               │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ HTTPS (SigV4 Auth)
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                           AWS Gateway (Port 9999)                           │
│                                                                             │
│   • TLS termination                    • AWS SigV4 authentication           │
│   • EC2 Query Protocol parsing         • Routes to service handlers         │
│   • XML response generation            • NATS client connection             │
│                                                                             │
│   File: hive/services/awsgw/awsgw.go                                        │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ JSON over NATS
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            NATS Message Broker                              │
│                                                                             │
│   Topics:                              Queue Groups:                        │
│   • ec2.RunInstances                   • hive-workers (load balanced)       │
│   • ec2.DescribeInstances              • No queue (fan-out to all nodes)    │
│   • ec2.StartInstances                                                      │
│   • ec2.StopInstances                                                       │
│   • ec2.TerminateInstances                                                  │
│   • ebs.mount / ebs.unmount                                                 │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                    ┌─────────────────┼─────────────────┐
                    ▼                 ▼                 ▼
┌──────────────────────┐ ┌──────────────────────┐ ┌──────────────────────┐
│   Hive Daemon        │ │   Hive Daemon        │ │   Hive Daemon        │
│   (Node 1)           │ │   (Node 2)           │ │   (Node N)           │
│                      │ │                      │ │                      │
│ • NATS subscriber    │ │ • NATS subscriber    │ │ • NATS subscriber    │
│ • Resource manager   │ │ • Resource manager   │ │ • Resource manager   │
│ • QEMU/KVM launcher  │ │ • QEMU/KVM launcher  │ │ • QEMU/KVM launcher  │
│ • QMP monitoring     │ │ • QMP monitoring     │ │ • QMP monitoring     │
│                      │ │                      │ │                      │
│ File: daemon.go      │ │ File: daemon.go      │ │ File: daemon.go      │
└──────────────────────┘ └──────────────────────┘ └──────────────────────┘
          │                       │                       │
          ▼                       ▼                       ▼
┌──────────────────────┐ ┌──────────────────────┐ ┌──────────────────────┐
│   QEMU/KVM VMs       │ │   QEMU/KVM VMs       │ │   QEMU/KVM VMs       │
└──────────────────────┘ └──────────────────────┘ └──────────────────────┘
```

## Request Flow

### 1. AWS SDK Request

Users interact with Hive using standard AWS SDKs or the AWS CLI by pointing to a custom endpoint:

```bash
aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 run-instances \
    --image-id ami-debian12 \
    --instance-type t3.micro \
    --key-name my-keypair
```

The AWS SDK formats this as an HTTP POST with:
- AWS SigV4 authentication headers
- EC2 Query Protocol body (Action=RunInstances&ImageId=ami-debian12...)

### 2. AWS Gateway

The gateway (`hive/services/awsgw/awsgw.go:57-107`) is the entry point:

```go
// awsgw.go:74 - Connect to NATS
natsConn, err := nats.Connect(nodeConfig.NATS.Host, opts...)

// awsgw.go:89-94 - Create gateway with NATS connection
gw := gateway.GatewayConfig{
    NATSConn: natsConn,
    Config:   nodeConfig.AWSGW.Config,
}

// awsgw.go:103 - Start TLS listener
log.Fatal(app.ListenTLS(nodeConfig.AWSGW.Host, nodeConfig.AWSGW.TLSCert, nodeConfig.AWSGW.TLSKey))
```

Request routing (`hive/gateway/gateway.go:125-156`):

1. **Authentication**: SigV4 middleware validates AWS credentials (`gateway.go:104`)
2. **Service Detection**: Extracts service name (ec2, iam, account) from Authorization header (`gateway.go:158-186`)
3. **Action Dispatch**: Routes to service-specific handler (`gateway.go:136-145`)

```go
// gateway.go:136-145
switch svc {
case "ec2":
    err = gw.EC2_Request(ctx)
case "account":
    err = gw.Account_Request(ctx)
case "iam":
    err = gw.IAM_Request(ctx)
}
```

### 3. EC2 Handler

The EC2 handler (`hive/gateway/ec2.go`) parses the Action parameter and delegates to specific handlers:

```go
// ec2.go - Action routing
switch action {
case "RunInstances":
    output, err := gateway_ec2_instance.RunInstances(input, gw.NATSConn)
case "DescribeInstances":
    output, err := gateway_ec2_instance.DescribeInstances(input, gw.NATSConn, expectedNodes)
// ... more actions
}
```

### 4. NATS Messaging

The gateway communicates with daemons via NATS request/response (`hive/handlers/ec2/instance/service_nats.go:24-57`):

```go
// service_nats.go:24-57
func (s *NATSInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
    // Marshal input to JSON
    jsonData, err := json.Marshal(input)

    // Send request to daemon via NATS with 5 minute timeout
    msg, err := s.natsConn.Request("ec2.RunInstances", jsonData, 5*time.Minute)

    // Unmarshal response
    var reservation ec2.Reservation
    err = json.Unmarshal(msg.Data, &reservation)

    return &reservation, nil
}
```

### 5. Daemon Processing

Daemons (`hive/daemon/daemon.go`) subscribe to NATS topics and handle requests:

```go
// daemon.go:319 - Subscribe with queue group for load balancing
d.natsSubscriptions["ec2.RunInstances"], err = d.natsConn.QueueSubscribe(
    "ec2.RunInstances",
    "hive-workers",      // Queue group - only one daemon handles each request
    d.handleEC2RunInstances,
)

// daemon.go:373 - Subscribe without queue group for fan-out
d.natsSubscriptions["ec2.DescribeInstances"], err = d.natsConn.Subscribe(
    "ec2.DescribeInstances",
    d.handleEC2DescribeInstances,  // All daemons respond
)
```

**Queue Groups**: Topics subscribed with a queue group (`hive-workers`) are load-balanced - only one daemon handles each request. Topics without a queue group fan out to all daemons.

### 6. VM Launch

When a daemon handles `RunInstances`:

1. **Resource Check**: Validates CPU/memory availability (`daemon.go:66-74`)
2. **Volume Generation**: Creates boot, cloud-init, and EFI volumes via Viperblock
3. **Volume Mount**: Sends `ebs.mount` request to Viperblock, receives NBD URI
4. **QEMU Launch**: Builds and executes QEMU command with NBD-backed disks
5. **QMP Monitoring**: Establishes QEMU Machine Protocol connection for VM management

```go
// daemon.go - Volume mounting via NATS
msg, err := d.natsConn.Request("ebs.mount", ebsMountRequest, 10*time.Second)

// daemon.go - QEMU launch with NBD storage
cmd := exec.Command("qemu-system-x86_64",
    "-drive", fmt.Sprintf("file=nbd:%s,format=raw", nbdURI),
    // ... additional QEMU args
)
```

## Key Components

### Daemon Structure

```go
// daemon.go:76-102
type Daemon struct {
    node            string                  // Node identifier
    clusterConfig   *config.ClusterConfig   // Cluster-wide configuration
    config          *config.Config          // Node-specific configuration
    natsConn        *nats.Conn              // NATS connection
    resourceMgr     *ResourceManager        // CPU/Memory tracking
    instanceService *InstanceServiceImpl    // EC2 instance operations
    keyService      *KeyServiceImpl         // SSH key operations
    imageService    *ImageServiceImpl       // AMI operations
    Instances       vm.Instances            // Local VMs
    clusterApp      *fiber.App              // HTTP cluster manager
}
```

### Resource Manager

Tracks available and allocated CPU/memory to prevent overcommit:

```go
// daemon.go:66-74
type ResourceManager struct {
    mu            sync.RWMutex
    availableVCPU int
    availableMem  float64
    allocatedVCPU int
    allocatedMem  float64
    instanceTypes map[string]InstanceType  // t3.micro, t3.small, etc.
}
```

### NATS Topics

| Topic | Queue Group | Purpose |
|-------|-------------|---------|
| `ec2.RunInstances` | `hive-workers` | Launch new instances |
| `ec2.DescribeInstances` | None (fan-out) | Query all nodes for instances |
| `ec2.StartInstances` | `hive-workers` | Start stopped instances |
| `ec2.StopInstances` | `hive-workers` | Stop running instances |
| `ec2.TerminateInstances` | `hive-workers` | Terminate instances |
| `ec2.CreateKeyPair` | `hive-workers` | Generate SSH keypair |
| `ec2.DescribeKeyPairs` | `hive-workers` | List SSH keypairs |
| `ec2.DescribeImages` | `hive-workers` | List AMIs |
| `ebs.mount` | - | Mount volume via Viperblock |
| `ebs.unmount` | - | Unmount volume |
| `hive.admin.{node}.health` | None | Node health checks |

### Multi-Node Aggregation

For operations that need data from all nodes (like `DescribeInstances`), the gateway uses inbox-based fan-out (`hive/gateway/ec2/instance/DescribeInstances.go`):

```go
// DescribeInstances.go:16-111
func DescribeInstances(...) {
    // Create unique inbox for collecting responses
    inbox := nats.NewInbox()
    sub, err := natsConn.SubscribeSync(inbox)

    // Publish to all nodes (no queue group = all daemons respond)
    err = natsConn.PublishRequest("ec2.DescribeInstances", inbox, jsonData)

    // Collect responses from all nodes with 3-second timeout
    for {
        msg, err := sub.NextMsg(3 * time.Second)
        allReservations = append(allReservations, nodeOutput.Reservations...)
    }

    return allReservations
}
```

## Storage Integration

### Viperblock (EBS)

Block storage volumes are managed via NATS messages to Viperblock:

```go
// Volume mount request
type EBSRequest struct {
    Name      string  // Volume name (vol-xxxxx)
    VolType   string  // gp2, io1, etc.
    Boot      bool    // Boot volume flag
    EFI       bool    // EFI boot volume
    CloudInit bool    // Cloud-init volume
    NBDURI    string  // NBD URI returned from mount
}
```

Viperblock responds with an NBD URI that QEMU uses to access the block device.

### Predastore (S3)

Object storage used for:
- AMI image storage
- SSH public key storage (`/bucket/ec2/{key-name}.pub`)
- Volume metadata and configuration

## Configuration

Cluster configuration (`hive/config/config.go`):

```go
type ClusterConfig struct {
    Epoch   uint64            // Incremented on config changes
    Node    string            // This node's identifier
    Version string            // Hive version
    Nodes   map[string]Config // Configuration for all nodes
}

type Config struct {
    Node, Host, Region, AZ string
    DataDir    string
    Daemon     DaemonConfig     // Cluster manager port
    NATS       NATSConfig       // NATS broker address
    Predastore PredastoreConfig // S3 endpoint
    AWSGW      AWSGWConfig      // Gateway port, TLS certs
    AccessKey, SecretKey string
}
```

## File Structure

```
hive/
├── hive/
│   ├── services/
│   │   └── awsgw/
│   │       └── awsgw.go        # AWS Gateway service entry point
│   ├── gateway/
│   │   ├── gateway.go          # Request routing, auth middleware
│   │   ├── ec2.go              # EC2 action dispatcher
│   │   └── ec2/
│   │       └── instance/
│   │           ├── RunInstances.go
│   │           └── DescribeInstances.go
│   ├── handlers/
│   │   └── ec2/
│   │       ├── instance/
│   │       │   ├── service_nats.go  # NATS request/response
│   │       │   └── service_impl.go  # Business logic
│   │       ├── key/
│   │       └── image/
│   ├── daemon/
│   │   └── daemon.go           # Daemon entry point, NATS subscriptions
│   ├── config/
│   │   └── config.go           # Configuration structures
│   ├── vm/
│   │   └── instance.go         # VM instance representation
│   └── qmp/
│       └── qmp.go              # QEMU Machine Protocol client
└── cmd/
    └── hive/
        └── main.go             # CLI entry point
```

## Development

### Running the Stack

1. Start NATS broker
2. Start Predastore (S3)
3. Start Viperblock (EBS)
4. Start Hive Gateway (`hive awsgw`)
5. Start Hive Daemon(s) (`hive daemon`)

### Testing with AWS CLI

```bash
# Set endpoint
export AWS_ENDPOINT_URL=https://localhost:9999

# Run instance
aws --no-verify-ssl ec2 run-instances \
    --image-id ami-debian12 \
    --instance-type t3.micro

# List instances (queries all nodes)
aws --no-verify-ssl ec2 describe-instances

# Terminate instance
aws --no-verify-ssl ec2 terminate-instances --instance-ids i-xxxxx
```
