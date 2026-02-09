# Hive Development Roadmap

## Known Bugs

- **QueryParamsToStruct does not match all AWS query param key formats**: The parser tries the Go field name (e.g. `AttributeNames.1`) and the SDK `locationName` tag (e.g. `attributeName.1`), but the AWS CLI sends PascalCase singular form (e.g. `AttributeName.1`). This causes list filters like `--attribute-names` on `describe-account-attributes` to be silently ignored. Affects any SDK field where the Go name, locationName, and wire format all differ. Fix in `hive/awsec2query/parser.go`.

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

## AWS Command Implementation Matrix

### EC2 - Instance Management

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `run-instances` | `--image-id`, `--instance-type`, `--count` (Min/MaxCount), `--key-name`, `--user-data`, `--block-device-mappings` (DeviceName, VolumeSize, VolumeType, Iops, DeleteOnTermination) | `--security-group-ids`, `--subnet-id`, `--placement`, `--tag-specifications`, `--dry-run`, `--client-token`, `--disable-api-termination`, `--ebs-optimized`, `--iam-instance-profile`, `--network-interfaces`, `--private-ip-address`, `--monitoring`, `--credit-specification`, `--cpu-options`, `--metadata-options`, `--launch-template`, `--hibernate-options` | `describe-images` (AMI must exist), `create-key-pair` (optional), VPC/SG (optional) | Gateway parses AWS query → NATS `ec2.runinstances` → daemon creates QEMU/KVM VM with viperblock-backed root volume via NBD → cloud-init injects user-data/keys → returns reservation with instance ID | 1. Launch with valid AMI and key pair<br>2. Launch with invalid AMI ID (error)<br>3. Launch with block device mappings (custom volume size)<br>4. Launch multiple instances (MinCount/MaxCount)<br>5. DryRun returns validation-only<br>6. Invalid instance type returns error | **DONE** |
| `describe-instances` | `--instance-ids` | `--filters`, `--max-results`, `--next-token`, `--dry-run` | None | Gateway fans out NATS `ec2.DescribeInstances` to all nodes (no queue group) → each daemon returns local instances → gateway aggregates and returns combined list | 1. Describe all instances (no filter)<br>2. Describe by instance ID<br>3. Describe with filters (e.g. instance-state-name)<br>4. Instance not found returns empty set<br>5. Multi-node aggregation returns instances from all nodes | **DONE** |
| `start-instances` | `--instance-ids` | `--dry-run`, `--force` | `run-instances` (instance must exist in stopped state) | Gateway sends NATS `ec2.cmd.{instance-id}` → daemon restarts stopped QEMU process with same config → state transitions stopped→pending→running | 1. Start a stopped instance<br>2. Start already-running instance (error: IncorrectInstanceState)<br>3. Start with invalid instance ID<br>4. Verify volumes re-mount on start | **DONE** |
| `stop-instances` | `--instance-ids` | `--force`, `--hibernate`, `--dry-run` | `run-instances` (instance must be running) | Gateway sends NATS to target node → daemon issues QMP `system_powerdown` for graceful shutdown → monitors heartbeat until QEMU exits → state transitions running→stopping→stopped | 1. Graceful stop of running instance<br>2. Force stop (kills QEMU process)<br>3. Stop already-stopped instance (error)<br>4. Verify ~30s heartbeat detection | **DONE** |
| `terminate-instances` | `--instance-ids`, `DeleteOnTermination` (per-volume flag, default true) | `--dry-run` | `run-instances` (instance must exist) | Gateway sends NATS to target node → daemon kills QEMU process → cleans up NBD mounts → deletes volumes with `DeleteOnTermination=true` via `volumeService.DeleteVolume()` (S3 cleanup of vol/, vol-efi/, vol-cloudinit/) → internal volumes (EFI, cloud-init) always cleaned up via `ebs.delete` NATS → volumes with `DeleteOnTermination=false` left in available state → state→terminated | 1. Terminate running instance<br>2. Terminate stopped instance<br>3. Terminate with DeleteOnTermination=true deletes volumes<br>4. Terminate with DeleteOnTermination=false preserves volumes<br>5. Terminate already-terminated (idempotent)<br>6. Internal volumes (EFI, cloud-init) always cleaned up<br>7. Invalid instance ID | **DONE** |
| `reboot-instances` | — | `--instance-ids`, `--dry-run` | `run-instances` (instance must be running) | Gateway sends NATS to target node → daemon issues QMP `system_reset` → instance reboots without stopping | 1. Reboot running instance<br>2. Reboot stopped instance (error)<br>3. Verify instance stays in running state after reboot | **NOT STARTED** (parser test exists) |
| `describe-instance-types` | `--filters` (capacity filter only) | `--instance-types`, `--max-results`, `--next-token`, `--dry-run`, all other filters | None | Gateway fans out NATS `ec2.DescribeInstanceTypes` to all nodes → each daemon reports supported types (t3.micro/small/medium/large) with vCPU/memory specs → gateway deduplicates and returns | 1. List all instance types<br>2. Filter by specific type<br>3. Filter with `capacity=true` shows available slots<br>4. Verify vCPU/memory specs match hardware | **DONE** |
| `modify-instance-attribute` | — | `--instance-id`, `--instance-type`, `--user-data`, `--disable-api-termination`, `--ebs-optimized` | Instance must be stopped | Validate instance stopped → update instance metadata in NATS KV store → changes applied on next start | 1. Change instance type while stopped<br>2. Modify running instance (error)<br>3. Toggle disable-api-termination | **NOT STARTED** |
| `get-console-output` | — | `--instance-id`, `--latest` | Instance must exist | QMP command to read serial console ring buffer → return base64-encoded output with timestamp | 1. Get output from running instance<br>2. Get output from stopped instance<br>3. Verify cloud-init boot logs visible | **NOT STARTED** |
| `monitor-instances` | — | `--instance-ids` | Instance must exist | Enable basic monitoring (CPU, disk, network) → store metrics in NATS KV → return monitoring state | 1. Enable monitoring<br>2. Verify monitoring state in describe-instances | **NOT STARTED** |

### EC2 - Key Pair Management

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-key-pair` | `--key-name`, `--key-type` (rsa/ed25519) | `--key-format` (pem/ppk), `--tag-specifications`, `--dry-run` | None | NATS `ec2.CreateKeyPair` → daemon generates SSH keypair → stores public key in Predastore S3 (`/bucket/ec2/{name}.pub`) → returns private key material (one-time) | 1. Create RSA key pair<br>2. Create ED25519 key pair<br>3. Duplicate key name (error: InvalidKeyPair.Duplicate)<br>4. Verify key material returned only on creation | **DONE** |
| `describe-key-pairs` | `--key-names`, `--key-pair-ids` | `--filters`, `--max-results`, `--dry-run` | None | NATS `ec2.DescribeKeyPairs` → daemon lists public keys from Predastore S3 → returns key names and fingerprints | 1. List all key pairs<br>2. Filter by key name<br>3. Filter by key pair ID<br>4. Non-existent key returns empty | **DONE** |
| `delete-key-pair` | `--key-name`, `--key-pair-id` | `--dry-run` | Key must exist | NATS `ec2.DeleteKeyPair` → daemon deletes public key from Predastore S3 → returns success | 1. Delete existing key pair<br>2. Delete non-existent key (idempotent, no error)<br>3. Verify key no longer in describe-key-pairs | **DONE** |
| `import-key-pair` | `--key-name`, `--public-key-material` | `--tag-specifications`, `--dry-run` | None | NATS `ec2.ImportKeyPair` → daemon stores provided public key in Predastore S3 → returns key name and fingerprint | 1. Import valid SSH public key<br>2. Import invalid key material (error)<br>3. Import duplicate name (error)<br>4. Verify imported key usable with run-instances | **DONE** |

### EC2 - AMI Image Management

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `describe-images` | `--image-ids` (format validation only) | `--owners`, `--executable-users`, `--filters`, `--include-deprecated`, `--include-disabled`, `--max-results`, `--next-token`, `--dry-run` | None | NATS `ec2.DescribeImages` → daemon reads AMI metadata from Predastore S3 buckets (ami-*) → returns image list with state, architecture, block device mappings | 1. List all images<br>2. Filter by image ID<br>3. Filter by owner (self/amazon)<br>4. Non-existent AMI returns empty<br>5. Verify metadata fields (architecture, state, rootDeviceName) | **DONE** |
| `create-image` | — | `--instance-id`, `--name`, `--description`, `--no-reboot`, `--block-device-mappings`, `--tag-specifications` | Instance must exist | Stop instance (unless --no-reboot) → snapshot root volume via viperblock → create AMI metadata in S3 → register as new AMI → restart instance → return ami-ID | 1. Create image from running instance<br>2. Create with --no-reboot<br>3. Invalid instance ID (error)<br>4. Verify new AMI appears in describe-images<br>5. Launch new instance from created AMI | **NOT STARTED** |
| `register-image` | — | `--name`, `--description`, `--architecture`, `--root-device-name`, `--virtualization-type`, `--block-device-mappings`, `--boot-mode`, `--ena-support`, `--sriov-net-support`, `--tpm-support`, `--imds-support` | Image data must exist in S3 | Create AMI metadata entry in Predastore → assign ami-ID → set state to available → return ami-ID | 1. Register with valid metadata<br>2. Missing required name (error)<br>3. Verify registered image in describe-images | **NOT STARTED** |
| `deregister-image` | — | `--image-id`, `--dry-run` | AMI must exist, no instances using it | Mark AMI metadata as deregistered in S3 → AMI no longer available for new launches → existing instances unaffected | 1. Deregister existing AMI<br>2. Deregister non-existent AMI (error)<br>3. Verify deregistered AMI not in describe-images<br>4. Existing instances from AMI still run | **NOT STARTED** |
| `copy-image` | — | `--source-image-id`, `--source-region`, `--name`, `--description`, `--encrypted`, `--kms-key-id`, `--client-token`, `--copy-image-tags`, `--tag-specifications` | Source AMI must exist | Copy AMI data between regions/nodes in Predastore → create new AMI metadata with new ami-ID → return new ami-ID | 1. Copy image within same region<br>2. Copy non-existent image (error)<br>3. Verify copied image independent of source | **NOT STARTED** |
| `import-image` | — | `--disk-containers` (Format, Url/S3Bucket+S3Key), `--description`, `--architecture`, `--platform` | S3 bucket with disk image | Download disk image from S3 → convert format (VMDK/VHD/RAW→QCOW2) → create viperblock volume → register as AMI | 1. Import QCOW2 image<br>2. Import RAW image<br>3. Invalid format (error)<br>4. Verify imported image launchable | **NOT STARTED** |
| `describe-image-attribute` | — | `--image-id`, `--attribute`, `--dry-run` | AMI must exist | Read specific attribute from AMI metadata in S3 → return attribute value | 1. Get description attribute<br>2. Get launch permission<br>3. Invalid attribute name (error) | **NOT STARTED** |
| `modify-image-attribute` | — | `--image-id`, `--launch-permission`, `--description`, `--operation-type`, `--user-ids`, `--user-groups`, `--organization-arns`, `--dry-run` | AMI must exist | Update specific attribute in AMI metadata → persist to S3 | 1. Add launch permission<br>2. Modify description<br>3. Invalid AMI (error) | **NOT STARTED** |
| `reset-image-attribute` | — | `--image-id`, `--attribute`, `--dry-run` | AMI must exist | Reset attribute to default value in AMI metadata | 1. Reset launch permission to owner-only<br>2. Invalid AMI (error) | **NOT STARTED** |

### EC2 - Volume (EBS) Management

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `describe-volumes` | `--volume-ids` (fast-path lookup), `DeleteOnTermination` (from persisted VolumeMetadata) | `--filters`, `--max-results`, `--next-token`, `--dry-run` | None | NATS `ec2.DescribeVolumes` → daemon queries viperblock for volume metadata → returns volume list with state, size, attachments, type, DeleteOnTermination flag | 1. List all volumes<br>2. Filter by volume ID<br>3. Filter by attachment state<br>4. Non-existent volume returns empty<br>5. DeleteOnTermination reflects persisted value | **DONE** |
| `modify-volume` | `--volume-id`, `--size`, `--volume-type`, `--iops` | `--throughput`, `--dry-run`, `--multi-attach-enabled` | Volume must exist | NATS `ec2.ModifyVolume` → daemon sends resize request to viperblock → NBD does not support live resize, instance must be stopped → returns modification state | 1. Increase volume size<br>2. Modify volume type<br>3. Decrease size (error - not supported)<br>4. Modify attached volume (requires stop/start) | **DONE** |
| `create-volume` | `--size`, `--availability-zone`, `--volume-type` (gp3 only) | `--iops`, `--encrypted`, `--snapshot-id`, `--tag-specifications` | Valid AZ configured via `hive init` | Gateway validates input → NATS `ec2.CreateVolume` → daemon generates vol-ID via viperblock → creates empty volume of specified size → persists config.json to Predastore S3 → returns vol-ID with state=available | 1. Create 80GB gp3 volume<br>2. Boundary sizes (1 GiB min, 16384 GiB max)<br>3. Invalid AZ (error)<br>4. Verify volume in describe-volumes<br>5. Unsupported volume type (error - only gp3)<br>6. Size out of range (error) | **DONE** |
| `delete-volume` | `--volume-id` | `--dry-run` | Volume must exist and be detached (state=available) | Gateway validates vol- prefix → NATS `ec2.DeleteVolume` → daemon confirms state=available and no AttachedInstance → NATS `ebs.delete` to viperblockd (stops nbdkit/WAL) → deletes S3 objects under vol-id/, vol-id-efi/, vol-id-cloudinit/ → returns success | 1. Delete detached volume<br>2. Delete attached volume (error: VolumeInUse)<br>3. Delete non-existent volume (error: InvalidVolume.NotFound)<br>4. Verify volume gone from describe-volumes<br>5. Malformed volume ID (error: InvalidVolumeID.Malformed)<br>6. Double delete (idempotent NotFound) | **DONE** |
| `attach-volume` | `--volume-id`, `--instance-id`, `--device` (optional, auto-assigns `/dev/sd[f-p]`) | `--dry-run` | Volume must exist (available), instance must exist (running) | Gateway sends to `ec2.cmd.{instanceId}` → daemon validates volume (Predastore) → `ebs.mount` via NATS (viperblock starts NBD server) → QMP `blockdev-add` (nbd-{volId}) → QMP `device_add` (virtio-blk-pci, vdisk-{volId}) → three-phase rollback on failure → update EBSRequests + BlockDeviceMappings → persist to JetStream + Predastore → respond with VolumeAttachment | 1. Attach volume to running instance<br>2. Auto-assign device name<br>3. Attach already-attached volume (VolumeInUse)<br>4. Attach to non-existent instance (InvalidInstanceID.NotFound)<br>5. Attach to stopped instance (IncorrectInstanceState)<br>6. Volume not found (InvalidVolume.NotFound)<br>7. All device slots full (AttachmentLimitExceeded)<br>8. Volume persists across stop/start | **DONE** |
| `detach-volume` | `--volume-id`, `--instance-id` (optional, resolved via DescribeVolumes), `--device` (optional cross-check), `--force` | `--dry-run` | Volume must be attached, instance must be running | Gateway resolves InstanceId if omitted (via DescribeVolumes) → sends to `ec2.cmd.{instanceId}` → daemon validates (running, attached, not boot/EFI/CloudInit, device match) → three-phase hot-unplug: QMP `device_del` (force continues on failure) → QMP `blockdev-del` (abort if fails, preserves state to prevent double-attach) → `ebs.unmount` via NATS (best-effort) → remove from EBSRequests + BlockDeviceMappings → update volume metadata to available → persist state → respond with VolumeAttachment (state=detaching) | 1. Detach with explicit InstanceId<br>2. Detach without InstanceId (gateway resolution)<br>3. Detach with correct --device cross-check<br>4. Missing VolumeId (InvalidParameterValue)<br>5. Volume not attached (IncorrectState)<br>6. Nonexistent volume (InvalidVolume.NotFound)<br>7. Nonexistent instance (InvalidInstanceID.NotFound)<br>8. Instance not running (IncorrectInstanceState)<br>9. Device mismatch (InvalidParameterValue)<br>10. Boot volume protection (OperationNotPermitted)<br>11. Force flag (continues past device_del failure)<br>12. Volume reusability (re-attach after detach) | **DONE** |
| `describe-volume-status` | `--volume-ids` | `--filters`, `--max-results`, `--next-token`, `--dry-run` | None | Gateway validates vol- prefix → NATS `ec2.DescribeVolumeStatus` → daemon fetches VolumeConfig from Predastore S3 (parallel for specific IDs, sequential list-all for no IDs) → builds VolumeStatusItem per volume (status=ok, io-enabled=passed, io-performance=not-applicable) → returns InvalidVolume.NotFound for missing explicit IDs → skips internal sub-volumes (-efi, -cloudinit) | 1. List all volume statuses<br>2. Filter by specific volume IDs (fast path)<br>3. Non-existent volume ID returns InvalidVolume.NotFound<br>4. Invalid volume ID format (InvalidVolume.Malformed)<br>5. Internal sub-volumes excluded from listing<br>6. Nil/empty input defaults to all volumes | **DONE** |
| `describe-volumes-modifications` | — | `--volume-ids`, `--filters`, `--max-results` | None | Query pending/completed volume modifications → return modification state, progress, original/target size | 1. Check in-progress modification<br>2. Check completed modification<br>3. No modifications returns empty | **NOT STARTED** |

### EC2 - Snapshot Management

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-snapshot` | — | `--volume-id`, `--description`, `--tag-specifications` | Volume must exist | Trigger viperblock WAL checkpoint → copy volume data to snapshot in Predastore → assign snap-ID → return snapshot with state=pending→completed | 1. Snapshot attached volume<br>2. Snapshot detached volume<br>3. Invalid volume ID (error)<br>4. Verify snapshot in describe-snapshots | **NOT STARTED** |
| `create-snapshots` | — | `--instance-specification`, `--description`, `--tag-specifications` | Instance must exist | Create snapshots of all volumes attached to instance → return list of snapshot IDs | 1. Snapshot all volumes on instance<br>2. Instance with no volumes | **NOT STARTED** |
| `delete-snapshot` | — | `--snapshot-id` | Snapshot must exist, not in use by AMI | Delete snapshot data from Predastore → return success | 1. Delete existing snapshot<br>2. Delete snapshot used by AMI (error)<br>3. Delete non-existent snapshot (error) | **NOT STARTED** |
| `describe-snapshots` | — | `--snapshot-ids`, `--owner-ids`, `--filters`, `--max-results` | None | Query Predastore for snapshot metadata → return snapshot list with state, volume ID, size, progress | 1. List all snapshots<br>2. Filter by snapshot ID<br>3. Filter by volume ID | **NOT STARTED** |
| `copy-snapshot` | — | `--source-snapshot-id`, `--source-region`, `--description`, `--encrypted` | Source snapshot must exist | Copy snapshot data between regions/nodes in Predastore → assign new snap-ID | 1. Copy within same region<br>2. Copy non-existent snapshot (error) | **NOT STARTED** |

### EC2 - Tags

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-tags` | — | `--resources`, `--tags` (Key=,Value=) | Resources must exist | Store tags in NATS KV or Predastore keyed by resource ID → return success | 1. Tag an instance<br>2. Tag a volume<br>3. Tag non-existent resource (error)<br>4. Overwrite existing tag | **NOT STARTED** |
| `delete-tags` | — | `--resources`, `--tags` | Resources must exist | Remove specified tags from resource metadata → return success | 1. Delete existing tag<br>2. Delete non-existent tag (idempotent) | **NOT STARTED** |
| `describe-tags` | — | `--filters`, `--max-results` | None | Query tag store with filters → return tag list with resource type and ID | 1. List all tags<br>2. Filter by resource type<br>3. Filter by key/value | **NOT STARTED** |

### EC2 - Regions & Availability Zones

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `describe-regions` | *(returns configured region, endpoint, opt-in status from config — input params ignored)* | `--region-names`, `--filters`, `--all-regions`, `--dry-run` | None | Return configured region from hive init config with Endpoint and OptInStatus → local-only response, no NATS | 1. List all regions<br>2. Filter by region name<br>3. Verify endpoint URL returned<br>4. Verify OptInStatus returned | **DONE** |
| `describe-availability-zones` | *(returns configured AZ, region, zone ID, state, opt-in status from config — input params ignored)* | `--zone-names`, `--filters`, `--all-availability-zones` | None | Return configured AZ from hive init config with zone ID, state, group name, network border group → local-only response, no NATS | 1. List all AZs<br>2. Filter by zone name<br>3. Verify zone state is available<br>4. Verify region name matches config | **DONE** |

### EC2 - Account Attributes

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `describe-account-attributes` | *(returns all 6 hardcoded attributes — input filter parsed but not applied due to QueryParamsToStruct bug, see Known Bugs)* | `--attribute-names` (filter not working) | None | Gateway parses input → returns static account attributes: supported-platforms=VPC, default-vpc=none, max-instances=100, vpc-max-security-groups-per-interface=5, max-elastic-ips=5, vpc-max-elastic-ips=20 → local-only response, no NATS | 1. List all account attributes<br>2. Filter by attribute name (blocked by parser bug)<br>3. Verify all 6 attributes returned with correct values | **DONE** |

### EC2 - VPC Networking (Core)

Requires Open vSwitch (`apt install openvswitch-switch openvswitch-common`). Single AZ for Hive v1.

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-vpc` | — | `--cidr-block`, `--tag-specifications`, `--instance-tenancy` | None | Create OVS bridge for VPC → assign vpc-ID → store VPC metadata in NATS KV → configure CIDR range → return VPC with state=available | 1. Create VPC with /16 CIDR<br>2. Overlapping CIDR (error)<br>3. Invalid CIDR format (error)<br>4. Verify in describe-vpcs | **NOT STARTED** |
| `delete-vpc` | — | `--vpc-id` | VPC must be empty (no subnets, gateways, instances) | Verify no dependent resources → delete OVS bridge → remove metadata → return success | 1. Delete empty VPC<br>2. Delete VPC with subnets (error: DependencyViolation)<br>3. Delete non-existent VPC (error) | **NOT STARTED** |
| `describe-vpcs` | — | `--vpc-ids`, `--filters`, `--max-results` | None | Query NATS KV for VPC metadata → return VPC list with CIDR, state, tags | 1. List all VPCs<br>2. Filter by VPC ID<br>3. Filter by CIDR block | **NOT STARTED** |
| `create-subnet` | — | `--vpc-id`, `--cidr-block`, `--availability-zone`, `--tag-specifications` | VPC must exist | Validate CIDR within VPC range → create OVS port group with VLAN tag → assign subnet-ID → store metadata | 1. Create subnet within VPC CIDR<br>2. Subnet CIDR outside VPC range (error)<br>3. Overlapping subnet CIDRs (error) | **NOT STARTED** |
| `delete-subnet` | — | `--subnet-id` | Subnet must be empty (no instances) | Verify no instances in subnet → remove OVS port group → delete metadata | 1. Delete empty subnet<br>2. Delete subnet with instances (error) | **NOT STARTED** |
| `describe-subnets` | — | `--subnet-ids`, `--filters`, `--max-results` | None | Query NATS KV for subnet metadata → return subnet list | 1. List all subnets<br>2. Filter by VPC ID<br>3. Filter by AZ | **NOT STARTED** |
| `create-security-group` | — | `--group-name`, `--description`, `--vpc-id`, `--tag-specifications` | VPC must exist | Create security group metadata → create default OVS flow rules (deny all inbound, allow all outbound) → assign sg-ID | 1. Create SG in VPC<br>2. Duplicate name in same VPC (error)<br>3. Verify default rules | **NOT STARTED** |
| `delete-security-group` | — | `--group-id` | SG must not be in use by instances | Verify no instances reference SG → remove OVS flow rules → delete metadata | 1. Delete unused SG<br>2. Delete SG in use (error) | **NOT STARTED** |
| `describe-security-groups` | — | `--group-ids`, `--group-names`, `--filters`, `--max-results` | None | Query NATS KV for SG metadata → return SG list with rules | 1. List all SGs<br>2. Filter by VPC ID<br>3. Filter by group name | **NOT STARTED** |
| `authorize-security-group-ingress` | — | `--group-id`, `--protocol`, `--port`, `--cidr`, `--source-group` | SG must exist | Add inbound rule → create OVS OpenFlow rule → persist to metadata | 1. Allow SSH (port 22) from 0.0.0.0/0<br>2. Allow from specific CIDR<br>3. Duplicate rule (idempotent) | **NOT STARTED** |
| `authorize-security-group-egress` | — | `--group-id`, `--protocol`, `--port`, `--cidr` | SG must exist | Add outbound rule → create OVS OpenFlow rule → persist to metadata | 1. Allow HTTPS outbound<br>2. Restrict to specific CIDR | **NOT STARTED** |
| `revoke-security-group-ingress` | — | `--group-id`, `--protocol`, `--port`, `--cidr` | SG must exist, rule must exist | Remove inbound rule → delete OVS OpenFlow rule → update metadata | 1. Revoke existing rule<br>2. Revoke non-existent rule (error) | **NOT STARTED** |
| `revoke-security-group-egress` | — | `--group-id`, `--protocol`, `--port`, `--cidr` | SG must exist, rule must exist | Remove outbound rule → delete OVS OpenFlow rule → update metadata | 1. Revoke existing rule<br>2. Revoke non-existent rule (error) | **NOT STARTED** |
| `create-internet-gateway` | — | `--tag-specifications` | None | Create IGW metadata → assign igw-ID → configure OVS NAT bridge for external access | 1. Create IGW<br>2. Verify in describe-internet-gateways | **NOT STARTED** |
| `attach-internet-gateway` | — | `--internet-gateway-id`, `--vpc-id` | IGW and VPC must exist | Link IGW to VPC → configure OVS flows for internet routing → update metadata | 1. Attach IGW to VPC<br>2. Attach already-attached IGW (error)<br>3. Attach to non-existent VPC (error) | **NOT STARTED** |
| `detach-internet-gateway` | — | `--internet-gateway-id`, `--vpc-id` | IGW must be attached to VPC | Unlink IGW from VPC → remove OVS internet routing flows | 1. Detach attached IGW<br>2. Detach unattached IGW (error) | **NOT STARTED** |
| `delete-internet-gateway` | — | `--internet-gateway-id` | IGW must be detached | Verify IGW detached → remove OVS NAT bridge → delete metadata | 1. Delete detached IGW<br>2. Delete attached IGW (error) | **NOT STARTED** |
| `describe-internet-gateways` | — | `--internet-gateway-ids`, `--filters`, `--max-results` | None | Query NATS KV for IGW metadata → return list with attachment info | 1. List all IGWs<br>2. Filter by attachment VPC | **NOT STARTED** |
| `create-route-table` | — | `--vpc-id`, `--tag-specifications` | VPC must exist | Create route table metadata → add default local route for VPC CIDR → assign rtb-ID | 1. Create route table<br>2. Verify default local route | **NOT STARTED** |
| `create-route` | — | `--route-table-id`, `--destination-cidr-block`, `--gateway-id` or `--nat-gateway-id` | Route table and target must exist | Add route entry → configure OVS flow for destination CIDR → update metadata | 1. Add internet route via IGW<br>2. Add route to NAT gateway<br>3. Conflicting route (error) | **NOT STARTED** |
| `describe-route-tables` | — | `--route-table-ids`, `--filters`, `--max-results` | None | Query NATS KV for route table metadata → return tables with routes | 1. List all route tables<br>2. Filter by VPC | **NOT STARTED** |
| `create-nat-gateway` | — | `--subnet-id`, `--allocation-id`, `--tag-specifications` | Subnet must exist, EIP must be allocated | Create NAT gateway in subnet → configure OVS SNAT rules → assign nat-ID → state=pending→available | 1. Create NAT gateway<br>2. Invalid subnet (error)<br>3. Verify NAT connectivity from private subnet | **NOT STARTED** |
| `allocate-address` | — | `--domain` (vpc), `--tag-specifications` | None | Allocate Elastic IP from pool → assign eipalloc-ID → store in metadata | 1. Allocate EIP<br>2. Verify in describe-addresses | **NOT STARTED** |
| `associate-address` | — | `--allocation-id`, `--instance-id` or `--network-interface-id` | EIP and target must exist | Associate EIP with instance/ENI → configure OVS DNAT/SNAT rules | 1. Associate with instance<br>2. Re-associate (moves EIP)<br>3. Associate non-existent EIP (error) | **NOT STARTED** |
| `release-address` | — | `--allocation-id` | EIP must exist and be disassociated | Release EIP back to pool → remove from metadata | 1. Release disassociated EIP<br>2. Release associated EIP (error) | **NOT STARTED** |
| `describe-addresses` | — | `--allocation-ids`, `--filters`, `--public-ips` | None | Query metadata for EIP allocations → return list with association info | 1. List all EIPs<br>2. Filter by allocation ID | **NOT STARTED** |

### EC2 - Network Interfaces

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `attach-network-interface` | — | `--network-interface-id`, `--instance-id`, `--device-index` | ENI and instance must exist | Attach OVS port to instance QEMU process → configure MAC/IP → update metadata | 1. Attach ENI to instance<br>2. Already attached (error) | **NOT STARTED** |
| `detach-network-interface` | — | `--attachment-id`, `--force` | ENI must be attached | Detach OVS port from instance → update metadata | 1. Detach ENI<br>2. Force detach | **NOT STARTED** |

### EC2 - Launch Templates

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-launch-template` | — | `--launch-template-name`, `--launch-template-data` (ImageId, InstanceType, etc.), `--tag-specifications` | None | Store template configuration in NATS KV → assign lt-ID → return template metadata | 1. Create template with full config<br>2. Duplicate name (error)<br>3. Verify in describe output | **NOT STARTED** |
| `describe-launch-templates` | — | `--launch-template-ids`, `--launch-template-names`, `--filters` | None | Query NATS KV for template metadata → return list | 1. List all templates<br>2. Filter by name | **NOT STARTED** |

### EBS Direct API

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `start-snapshot` | — | `--volume-size`, `--parent-snapshot-id`, `--description`, `--encrypted` | None | Initialize snapshot in viperblock → return snap-ID for block-level writes | 1. Start new snapshot<br>2. Start incremental from parent | **NOT STARTED** |
| `put-snapshot-block` | — | `--snapshot-id`, `--block-index`, `--block-data`, `--checksum` | start-snapshot | Write block data to snapshot at specified index in viperblock → verify checksum | 1. Write single block<br>2. Write with bad checksum (error) | **NOT STARTED** |
| `get-snapshot-block` | — | `--snapshot-id`, `--block-index` | Snapshot must exist | Read block data from snapshot at specified index → return data with checksum | 1. Read existing block<br>2. Read sparse block (zeros) | **NOT STARTED** |
| `complete-snapshot` | — | `--snapshot-id`, `--changed-blocks-count` | start-snapshot, put-snapshot-block | Finalize snapshot → mark as completed → sync to Predastore | 1. Complete valid snapshot<br>2. Complete with wrong block count (error) | **NOT STARTED** |
| `list-snapshot-blocks` | — | `--snapshot-id`, `--max-results`, `--next-token` | Snapshot must exist | List all non-empty blocks in snapshot with tokens and sizes | 1. List blocks of populated snapshot<br>2. List blocks of empty snapshot | **NOT STARTED** |
| `list-changed-blocks` | — | `--second-snapshot-id`, `--first-snapshot-id`, `--max-results` | Snapshots must exist | Compare two snapshots → return list of differing block indexes | 1. Compare parent and child snapshots<br>2. Compare unrelated snapshots | **NOT STARTED** |

### S3 (via Predastore)

Currently S3 operations are handled directly by Predastore. Consider moving control/data plane to Hive format for AWS gateway integration.

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `s3 mb` (CreateBucket) | — | `s3://bucket-name` | None | NATS `s3.createbucket` → Predastore creates bucket → return success | 1. Create valid bucket<br>2. Duplicate bucket name (error)<br>3. Invalid bucket name (error) | **NOT STARTED** (Predastore handles directly) |
| `s3 rb` (DeleteBucket) | — | `s3://bucket-name`, `--force` | Bucket must exist | NATS `s3.deletebucket` → verify bucket empty (or force) → Predastore deletes bucket | 1. Delete empty bucket<br>2. Delete non-empty bucket (error)<br>3. Force delete non-empty bucket | **NOT STARTED** |
| `s3 ls` (ListBuckets/Objects) | — | `s3://bucket/prefix`, `--recursive` | None | NATS `s3.listobjects` → Predastore lists objects with prefix → return list | 1. List all buckets<br>2. List objects in bucket<br>3. List with prefix filter | **NOT STARTED** |
| `s3 cp` (PutObject/GetObject) | — | `source dest`, `--recursive`, `--acl` | Bucket must exist | NATS `s3.putobject`/`s3.getobject` → Predastore stores/retrieves with Reed-Solomon encoding | 1. Upload single file<br>2. Download single file<br>3. Recursive upload/download<br>4. Large file (multipart) | **NOT STARTED** |
| `s3 rm` (DeleteObject) | — | `s3://bucket/key`, `--recursive` | Object must exist | NATS `s3.deleteobject` → Predastore deletes object shards | 1. Delete single object<br>2. Recursive delete<br>3. Delete non-existent (idempotent) | **NOT STARTED** |
| `s3 sync` | — | `source dest`, `--delete`, `--exclude`, `--include` | Bucket must exist | Compare source and dest → upload changed/new files → optionally delete removed files | 1. Sync local dir to S3<br>2. Sync with --delete<br>3. Sync with exclude filter | **NOT STARTED** |

### IAM (Planned)

| Command | Implemented Flags | Missing Flags | Prerequisites | Basic Logic | Test Cases | Status |
|---------|-------------------|---------------|---------------|-------------|------------|--------|
| `create-access-key` | — | `--user-name` | User must exist | Generate access key ID and secret → store in NATS JetStream KV → return credentials | 1. Create key for user<br>2. Max keys exceeded (error) | **NOT STARTED** (gateway stub exists) |
| `delete-access-key` | — | `--access-key-id`, `--user-name` | Key must exist | Remove access key from NATS KV → return success | 1. Delete existing key<br>2. Delete non-existent key (error) | **NOT STARTED** |
| `list-access-keys` | — | `--user-name`, `--max-items` | None | Query NATS KV for user's access keys → return list | 1. List keys for user<br>2. No keys returns empty | **NOT STARTED** |

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
