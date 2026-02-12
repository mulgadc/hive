# Hive Development Roadmap

## Update Jan 2026

### Big rocks:

- [DONE] Add multi-node support for predastore (S3) using reed solomon encoding
  - [DONE] Implement KV store for object lookup, WAL files, offset. Use hash-ring to determine which nodes an object belongs to.
- Implement VPC support using Open vSwitch across multiple nodes, core VPC functionality included
  - Add support for NVIDIA Bluefield DPU with Open vSwitch
- Implement basic IAM using NATS Jetstream as KV store, vs IAM/access-keys in local config/TOML files for beta.
  - [DONE] Move `daemon.go` instances.json state to Jetstream KV
- Add support using the `hive` CLI tool to provision a new user with AWS access-keys/IAM.
  - Support multi-tenant operations and isolation
- Add support to include capabilities when adding a new hardware node to MulgaOS (e.g EC2 target, S3, EBS, NATs, etc) - Features can be turned on/off depending on hardware scope.
- [DONE] Add simple Web UI console, using the AWS JS SDK, communicating to local AWS gateway.
  - [DONE] Implement ShadCNblocks for UI framework
  - [DONE] Simple Go webserver, static files, easy build process.

### Implementation gaps:

- EC2 - Support extended features for `run-instance`
  - [DONE] Volume resize of AMI. Note `nbd` does not support live resize events, instance needs to be stopped, resized, and started.
  - Confirm cloud-init will resize volume on boot and configured correctly.
  - [DONE] Attach additional volumes
  - Attach to VPC / Security group (required Open vSwitch implementation)
- only allow hive to use x% of cpu and memory. dont allow allocating 100% of resources.
- placement group instances, allow spreading instance group between nodes.
- capabilities flag
- more instance types - per cpu gen
- [DONE] multi part uploads on frontend
- [DONE] delete volumes on termination
- fix describe instances
- [DONE] change all log into slog
- `nbd` does not support resizing disks live. Requires instance to be stopped, boot/root volume, or attached volume will need to be resized, and instance started again. Limitation for Hive v1 using NBD, aiming to resolve with `vhost-user-blk` to create a `virtio-blk` device for extended functionality and performance.
- Migrate to net/http + chi from fiber
- remove insecure skip verify - need to add predastore multi node ips to certs
- migrate to list objects v2
- split up utils.go
- make sure nats is running before we start daemon? eigw and account service will fallback to memory versions but that breaks all functionality
- the BlockDeviceMappings > DeviceName (/dev/xvda) is not the true volume, i think it's /dev/vda on mine. I think qmp has a query device stats which will provide block devices so we can match these up. not a blocker, but needs to be fixed eventually
- aws ec2 modify-instance-attribute
- move configs into jetstream

### Issues to investigate

- When the host disk volume is full and a new instance is launched, disk corruption will occur within the instance since the host volume is out of space. Consider a pre-flight check, check available storage on the S3 server, local viperblock for WAL cache availability and provide improved guard rails to prevent a new instance running, if available disk space is nearing to be exceeded.

Host:

```json
{
  "time": "2026-01-27T21:30:29.368164556+10:00",
  "level": "ERROR",
  "msg": "WAL sync failed",
  "error": "write /home/hive/hive/predastore/distributed/nodes/node-2/0000000000000737.wal: no space left on device"
}
```

Instance `dmesg`:

```
[ 1445.573495] EXT4-fs error (device vda1): ext4_journal_check_start:83: comm systemd-journal: Detected aborted journal
[ 1445.583565] EXT4-fs (vda1): Remounting filesystem read-only
```

---



## Update Nov 2025

[PARTIAL] _ Implement multi-tenant support
[PARTIAL] _ Move config settings from ~/hive/\*.toml, to using Nats Jetstream for core config which can be synced between nodes.

- Implement a lightweight IAM using Nats Jetstream, vs current config files for auth settings.
  [ONGOING] _ Implement Reed Solomon Encoding for Predastore (S3) objects and a WAL implementation, for storing multiple objects in a single shard (e.g 4MB), with the WAL referencing the location of each object (e.g 4kb min)
  _ Implement basic KY lookup, object key sha512 (bucket/key), location to shard on S3 (obj.0000124.bin), read WAL (e.g first 4096 bytes) to determine location of the object. e.g key-1234 => obj.0000124.bin, wal header (4096 bytes) > key-1234 == offset location (seek) 16384, len 32768.
  _ Read multiple nodes (e.g 5 predastore instances, k = 3 (data shards), n = 5 (total shards), n - k = 2 (parity shards) )
  [ONGOING] _ Complete core scaffolding AWS SDK/API requirements (ec2 describe-instances, run-instances, etc)
- Implement UEFI support for image downloads and `qemu` exec in `vm.go`
- Confirm Alpine Linux, fails import image AMI > (run-instance) ""Failed to read block from AMI source" err="request out of range" - Block size correct?
- Improve shutdown gracefully, `./scripts/stop-dev.sh` waits 60 seconds, while qemu/nbd could still be shutting down.
- [DONE] Add delete-volume support via EBS (s3 vol-\*) for terminated instance (DeleteOnTermination flag)
- Add default LRU cache support for viperblock, depending on the instance type / volume size and system memory available.

## Multi-node setup

[DONE] - Development release - v0.1.0 (single node)
[PARTIAL] - Production release - v0.2.0 (multi node)

Design layout for multi-node configuration.

```
# node1
hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 3 --hive-dir ~/hive/

# node2
hive admin join --region ap-southeast-2 --az ap-southeast-2a --node node2 --host node1.local:8443 --data-dir ~/hive/

# node3, optionally toggle EBS/EC2/NATs support only
hive admin join --region ap-southeast-2 --az ap-southeast-2a --node node3 --host node1.local:8443 --data-dir ~/hive/ --cap ebs,ec2,nats
```

If --host is missing, the join command tries multicast broadcast to find parent (leader) node.

Set region with `--region ap-southeast-2` which will create a new Hive cluster for the specified region.

For production, it is recommended to run Hive on at least three physical nodes. In this minimum setup, Hive S3 and EBS volumes use 2 data shards and 1 parity shard RS(2,1) to distribute each chunk across the cluster. This configuration tolerates a full node failure while adding only 1.5x storage overhead.

### Overview:

- init creates a cluster-id, node-id, and a short join token, starts a tiny control server on :8443, and writes DNS hint files if available.

- init sets target size to 3 and waits until all 3 nodes join and ack.

- join nodes contact node1, present the token or cluster-id, and advertise capabilities.

- node1 appends them to the member set and immediately pushes the current settings bundle to them.

- When member count reaches 3, node1 broadcasts the final cluster settings to all nodes.

- Every node writes the same cluster.json and starts the services that match its capabilities.

Node exchange payload

```json
{
  "cluster_id": "c-82d5",
  "node_id": "n-rpi2",
  "addr": "rpi2.local:8443",
  "version": "0.1.0",
  "caps": ["ec2", "s3"],
  "ts": 1731388800
}
```

Settings bundle (identical on all 3 once committed)

```json
{
  "cluster_id": "c-82d5",
  "target_size": 3,
  "members": [
    {
      "node_id": "n-rpi1",
      "addr": "rpi1.local:8443",
      "caps": ["ec2", "s3", "nats", "ebs"]
    },
    { "node_id": "n-rpi2", "addr": "rpi2.local:8443", "caps": ["ec2", "s3"] },
    { "node_id": "n-rpi3", "addr": "rpi3.local:8443", "caps": ["nats", "ebs"] }
  ],
  "services": {
    "ec2": { "api_bind": ":9001" },
    "s3": { "api_bind": ":9002", "replicas": 2 },
    "nats": { "cluster": "c-82d5", "quorum": 2 },
    "ebs": { "replicas": 3, "block_size": 4096 }
  },
  "epoch": 1,
  "sig": "ed25519:..." // signed by rpi1 during init
}
```

## Original TODO Items → Development Plan Integration

### ✅ **Completed Integration**

All original TODO items have been incorporated into the structured development phases:

| Original TODO                         | Status     | Integrated Into                                | Phase     |
| ------------------------------------- | ---------- | ---------------------------------------------- | --------- |
| #1: Binary compile and install.sh     | ✅ Planned | Phase 7: Task 7.1 (Binary Compilation)         | Phase 7   |
| #2: Move daemon.go to services/hived/ | ✅ Planned | Phase 3: Task 3.2 (Specialized Services)       | Phase 3   |
| #3: VPC with openvs-switch as `vpcd`  | ✅ Planned | Phase 5: Task 5.1 (VPC with Open vSwitch)      | Phase 5   |
| #4: AWS HTTP gateway (`awsd`)         | ✅ Planned | Phase 3: Task 3.1 (AWS Gateway Service)        | Phase 3   |
| #5: AWS SDK v2 input/output           | ✅ Planned | Phase 2: Task 2.1 & Phase 3: Task 3.1          | Phase 2-3 |
| #6: `hive.service` for systemd        | ✅ Planned | Phase 7: Task 7.2 (System Service Integration) | Phase 7   |
| #7: Gossip and RAFT protocols         | ✅ Planned | Phase 0: Task 0.1 (Service Registry)           | Phase 0   |
| #8: etcd/KV for configuration sync    | ✅ Planned | Phase 0: Task 0.1 (Distributed Config)         | Phase 0   |
| #9: Smithy model code generation      | ✅ Planned | Phase 2: Task 2.1 (Smithy Code Generation)     | Phase 2   |

## Development Phase Overview

**Phase 0**: Distributed Systems Foundation (1-2 weeks)

- Gossip, RAFT, and distributed configuration (#7, #8)

**Phase 1**: Development Environment Automation (2-3 weeks)

- Multi-service orchestration and hot reloading

**Phase 2**: AWS API Model Implementation (3-4 weeks)

- Smithy-based code generation (#9)
- AWS SDK v2 integration (#5)

**Phase 3**: Scalable Gateway and Daemon Architecture (2-3 weeks)

- AWS gateway service `awsd` (#4)
- Service refactoring to `hived` (#2)
- VPC daemon `vpcd` foundation (#3)

**Phase 4**: Service Integration and Orchestration (3-4 weeks)

- NATS clustering and cross-service coordination

**Phase 5**: Infrastructure Services (4-5 weeks)

- VPC networking with Open vSwitch (#3)
- Advanced VM features

**Phase 6**: Testing and Validation Framework (2-3 weeks)

- AWS CLI compatibility testing

**Phase 7**: Production Deployment and Packaging (2-3 weeks)

- Binary compilation and distribution (#1)
- Systemd service integration (#6)
- Production configuration management

## Service Architecture

The development plan implements these services:

- **`awsd`** - AWS API Gateway (TODO #4)
- **`hived`** - Main Hive compute daemon (TODO #2)
- **`vpcd`** - VPC networking daemon (TODO #3)
- **Predastore** - S3 service (existing)
- **Viperblock** - EBS service (existing)

## Getting Started

For development setup:

```bash
./scripts/dev-setup.sh     # Setup complete development environment
./scripts/start-dev.sh     # Start all services
```

For detailed implementation guidance, see [HIVE_DEVELOPMENT_PLAN.md](HIVE_DEVELOPMENT_PLAN.md).
