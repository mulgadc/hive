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
  - Volume resize of AMI. Note `nbd` does not support live resize events, instance needs to be stopped, resized, and started.
  - Confirm cloud-init will resize volume on boot and configured correctly.
  - Attach additional volumes
  - Attach to VPC / Security group (required Open vSwitch implementation)

## EC2 describe-instances

Require to add additional meta-data for the attached volumes to a specified running instance.

```sh
aws ec2 describe-instances \
              --instance-ids i-1234567890abcdef0
```

```json
{
    "Reservations": [
        {
            "Groups": [],
            "Instances": [
                {
                  ....

                  "BlockDeviceMappings": [
                    {
                        "DeviceName": "/dev/xvda",
                        "Ebs": {
                            "AttachTime": "2022-11-15T10:49:00+00:00",
                            "DeleteOnTermination": true,
                            "Status": "attached",
                            "VolumeId": "vol-02e6ccdca7de29cf2"
                        }
                    }
                  ],

                }
            ]
        }
    ]
}
```


## EC2 create-volume

Allow the ability to create new volumes

```sh
aws ec2 create-volume \
    --size 80 \
    --availability-zone ap-southeast-2a
```

* viperblock - Create new volume for specified size (empty), push state to predastore.
* Note `gp3` volumes are only supported at this stage (no gp3, io1/io2, etc)
* Validate availability zone, only supports the specified value when running `hive init` - Multi AZ available Hive v2+

## EC2 attach-volume

```sh
aws ec2 attach-volume --volume-id vol-1234567890abcdef0 --instance-id i-01474ef662b89480 --device /dev/sdf
```

* Validate volume exists, e.g create-volume
* Push attach-volume request via NATS to the node running the specified instance - Run nbdkit for new volume, retrieve host:port or UNIX socket pathname, append volume to instance state, so will mount/attach on instance stop/start.
* Issue a command via QMP for the instance, to attach the volume, e.g
* Confirm if --device compatible, since NBD will export the drive, likely the host OS will decide the /dev/volume attributes.
* Volume available to mount within the guest OS. Volume meta-data attached to instance.json/NATS KV and available if instance stop/started.

```json
{
  "execute": "blockdev-add",
  "arguments": {
    "node-name": "nbd0",
    "driver": "nbd",
    "server": { "type": "unix", "path": "/run/nbd/vol0.sock" },
    "export": "", // Note, export volume should be blank for viperblock
    "read-only": false
  }
}
```

Once attached, QMP event to add the device. Likely --device argument can be specified here for /dev/block device. If --device missing, need to loop through the instance attached volumes and determine an id, e.g /dev/sda used already, /dev/sdb available.

```json
{
  "execute": "device_add",
  "arguments": {
    "driver": "virtio-blk-pci",
    "id": "vdisk1",
    "drive": "nbd0"
  }
}
```

## EC2 detach-volume

```sh
aws ec2 detach-volume --volume-id vol-1234567890abcdef0
```

* Fire NATS event to node running specified instance to detach the volume
* Use QMP to detach specified volume
* Terminate running `nbdkit` process for specified volume, ensure WAL state synced to predastore.

## EC2 delete-volume

Allow a volume to be deleted once detached from any running instances.

```sh
aws ec2 delete-volume --volume-id vol-049df61146c4d7901
```

* Confirm volume detached
* Terminate running `nbdkit` process for specified volume, ensure WAL state synced to predastore.
* Delete volume in predastore

## EC2 modify-volume

Example increasing a volume to 64GiB

```sh
aws ec2 modify-volume --size 64 --volume-id vol-1234567890abcdef0
```

```json
{
    "VolumeModification": {
        "TargetSize": 64,
        "TargetVolumeType": "gp3",
        "ModificationState": "modifying",
        "VolumeId": " vol-1234567890abcdef0",
        "TargetIops": 100,
        "StartTime": "2019-05-17T11:27:19.000Z",
        "Progress": 0,
        "OriginalVolumeType": "gp3",
        "OriginalIops": 100,
        "OriginalSize": 32
    }
}
```

* Note `nbd` does not support resizing disks live. Requires instance to be stopped, boot/root volume, or attached volume will need to be resized, and instance started again. Limitation for Hive v1 using NBD, aiming to resolve with `vhost-user-blk` to create a `virtio-blk` device for extended functionality and performance.

### EC2 describe-volume-status

Required to track progress of new volumes created, deleted, detached or running.

```sh
aws ec2 describe-volume-status --volume-ids vol-1234567890abcdef0
```

```json
{
    "VolumeStatuses": [
        {
            "VolumeStatus": {
                "Status": "ok",
                "Details": [
                    {
                        "Status": "passed",
                        "Name": "io-enabled"
                    },
                    {
                        "Status": "not-applicable",
                        "Name": "io-performance"
                    }
                ]
            },
            "AvailabilityZone": "us-east-1a",
            "VolumeId": "vol-1234567890abcdef0",
            "Actions": [],
            "Events": []
        }
    ]
}
```

### Issues to investigate

- When the host disk volume is full and a new instance is launched, disk corruption will occur within the instance since the host volume is out of space. Consider a pre-flight check, check available storage on the S3 server, local viperblock for WAL cache availability and provide improved guard rails to prevent a new instance running, if available disk space is nearing to be exceeded.

Host:

```json
{"time":"2026-01-27T21:30:29.368164556+10:00","level":"ERROR","msg":"WAL sync failed","error":"write /home/hive/hive/predastore/distributed/nodes/node-2/0000000000000737.wal: no space left on device"}
```

Instance `dmesg`:

```
[ 1445.573495] EXT4-fs error (device vda1): ext4_journal_check_start:83: comm systemd-journal: Detected aborted journal
[ 1445.583565] EXT4-fs (vda1): Remounting filesystem read-only
```


### EC2

- [DONE] - describe-instances
- [DONE] - run-instances (count not implemented)
- [DONE] - start-instances
- [DONE] - stop-instances
- [DONE] - terminate-instances
- [DONE] - describe-instance-types
- [DONE] - create-key-pair
- [DONE] - delete-key-pair
- [DONE] - describe-key-pairs
- [DONE] - import-key-pair
- [DONE] - describe-images
- [DONE] - describe-regions
- [DONE] - describe-volumes

### To implement

Easier methods to implement

- attach-volume
- copy-image
- copy-snapshot
- copy-volumes
- create-image
- create-snapshot
- create-snapshots
- create-tags
- create-volume
- delete-snapshot
- describe-snapshots
- describe-subnets
- describe-tags
- describe-volume-attribute
- describe-volume-status
- detach-network-interface
- detach-volume
- get-console-output
- monitor-instances

TODO:

- allocate-address
- allocate-hosts
- assign-private-ip-addresses
- assign-private-nat-gateway-address
- associate-address
- associate-nat-gateway-address
- associate-route-server
- associate-route-table
- associate-security-group-vpc
- associate-subnet-cidr-block
- attach-internet-gateway
- attach-network-interface
- authorize-security-group-egress
- authorize-security-group-ingress
- create-customer-gateway
- create-default-subnet
- create-default-vpc
- create-dhcp-options
- create-egress-only-internet-gateway
- create-internet-gateway
- create-launch-template
- create-launch-template-version
- create-local-gateway-route
- create-local-gateway-route-table
- create-local-gateway-route-table-virtual-interface-group-association
- create-local-gateway-virtual-interface
- create-local-gateway-virtual-interface-group
- create-nat-gateway
- create-network-acl
- create-network-acl-entry
- create-public-ipv4-pool
- create-route
- create-route-server
- create-route-server-endpoint
- create-route-server-peer
- create-route-table
- create-security-group
- create-subnet
- create-subnet-cidr-reservation
- delete-coip-cidr
- delete-customer-gateway
- delete-dhcp-options
- delete-egress-only-internet-gateway
- delete-internet-gateway
- delete-local-gateway-route
- delete-local-gateway-route-table
- delete-local-gateway-route-table-virtual-interface-group-association
- delete-local-gateway-virtual-interface
- delete-local-gateway-virtual-interface-group
- delete-nat-gateway
- delete-network-acl
- delete-network-interface
- delete-network-interface-permission
- delete-public-ipv4-pool
- delete-route
- delete-route-server
- delete-route-server-endpoint
- delete-route-server-peer
- delete-route-table
- delete-security-group
- delete-subnet
- delete-subnet-cidr-reservation
- delete-tags
- delete-volume
- describe-account-attributes
- describe-addresses
- describe-customer-gateways
- describe-dhcp-options
- describe-egress-only-internet-gateways
- describe-hosts
- describe-internet-gateways
- describe-managed-prefix-lists
- describe-nat-gateways
- describe-network-acls
- describe-route-server-endpoints
- describe-route-server-peers
- describe-route-servers
- describe-route-tables
- describe-security-group-references
- describe-security-group-rules
- describe-security-groups
- describe-volumes-modifications
- detach-internet-gateway
- detach-vpn-gateway
- disable-image
- disable-route-server-propagation
- disable-serial-console-access
- enable-address-transfer
- enable-capacity-manager
- enable-ebs-encryption-by-default
- enable-image
- enable-image-block-public-access
- export-image
- get-console-screenshot
- get-instance-metadata-defaults
- get-instance-tpm-ek-pub
- get-instance-types-from-instance-requirements
- get-instance-uefi-data
- get-launch-template-data
- get-route-server-associations
- get-route-server-propagations
- get-route-server-routing-database
- get-security-groups-for-vpc
- get-serial-console-access-status
- get-snapshot-block-public-access-state
- get-spot-placement-scores
- get-subnet-cidr-reservations
- import-image
- import-snapshot
- modify-instance-attribute
- modify-instance-capacity-reservation-attributes
- modify-instance-connect-endpoint
- modify-instance-cpu-options
- modify-instance-credit-specification
- modify-instance-placement
- modify-network-interface-attribute
- modify-volume
- modify-volume-attribute
- modify-vpn-connection
- modify-vpn-connection-options
- reboot-instances
- register-image
- release-address
- release-hosts
- replace-network-acl-association
- replace-network-acl-entry
- replace-route
- replace-route-table-association
- report-instance-status
- revoke-security-group-egress
- revoke-security-group-ingress
- run-instances

### EBS

- complete-snapshot
- get-snapshot-block
- list-changed-blocks
- list-snapshot-blocks
- put-snapshot-block
- start-snapshot

### VPC

- create-vpc
- create-vpc-block-public-access-exclusion
- create-vpc-encryption-control
- create-vpc-endpoint
- create-vpc-endpoint-connection-notification
- create-vpc-endpoint-service-configuration
- create-vpc-peering-connection
- delete-vpc
- delete-vpc-block-public-access-exclusion
- delete-vpc-encryption-control
- delete-vpc-endpoint-connection-notifications
- delete-vpc-endpoint-service-configurations
- delete-vpc-endpoints
- delete-vpc-peering-connection
- associate-vpc-cidr-block
- delete-local-gateway-route-table-vpc-association
- create-local-gateway-route-table-vpc-association
- describe-security-group-vpc-associations
- describe-vpc-attribute
- describe-vpc-block-public-access-exclusions
- describe-vpc-block-public-access-options
- describe-vpc-encryption-controls
- describe-vpc-endpoint-associations
- describe-vpc-endpoint-connection-notifications
- describe-vpc-endpoint-connections
- describe-vpc-endpoint-service-configurations
- describe-vpc-endpoint-service-permissions
- describe-vpc-endpoint-services
- describe-vpc-endpoints
- describe-vpc-peering-connections
- describe-vpcs
- modify-vpc-attribute
- modify-vpc-encryption-control
- modify-vpc-endpoint
- modify-vpc-endpoint-connection-notification
- modify-vpc-endpoint-service-configuration
- modify-vpc-tenancy

### S3

- Consider moving S3 control/data plane, from predastore, to Hive format.

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
- Add delete-volume support via EBS (s3 vol-\*) for terminated instance
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
