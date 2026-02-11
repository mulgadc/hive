# E2E Test Coverage

## Single-Node (`run-e2e.sh`)

### Phase 1: Environment Setup
- KVM support check
- `hive admin init` (region/az/node config)
- CA certificate trust
- Start all services (`start-dev.sh`)
- Wait for AWS gateway on `localhost:9999`

### Phase 2: Discovery & Metadata
- `describe-regions`
- `describe-availability-zones` (verify zone name and state)
- `describe-instance-types` (discover available types)
- Select nano instance type and detect architecture

### Phase 3: SSH Key Management
- `create-key-pair` (test-key-1, verify private key material)
- `import-key-pair` (test-key-2, from local RSA key)
- `describe-key-pairs` (verify both keys exist)
- `delete-key-pair` (test-key-2, verify only test-key-1 remains)

### Phase 4: Image Management
- `hive admin images import` (file-based, architecture-aware)
- `describe-images` (verify AMI by ID)

### Phase 5: Instance Lifecycle
- `run-instances` (launch VM with key pair)
- `describe-instances` (poll pending -> running)

### Phase 5a: Instance Metadata Validation
- `describe-instances` — verify InstanceType matches requested type
- Verify KeyName matches requested key
- Verify ImageId matches requested AMI
- Verify at least 1 BlockDeviceMapping present

### Phase 5 (cont): Root Volume
- `describe-volumes` (verify root volume attached)

### Phase 5b: Volume Lifecycle
- `create-volume` (10GB, ap-southeast-2a)
- `modify-volume` (resize to 20GB, poll to verify)
- `attach-volume` (to running instance, /dev/sdf)
- `describe-volumes` (verify in-use + attached state)
- `detach-volume` (verify available state)
- `delete-volume` (verify gone)

### Phase 5b-ii: DescribeVolumeStatus
- `describe-volume-status` (on root volume, verify VolumeId in response)

### Phase 5c: Snapshot Lifecycle
- Uses root volume already attached to running instance (snapshots require a mounted VB instance)
- `create-snapshot` (from attached root volume, with description)
- Verify create response fields (VolumeId, VolumeSize, State, Progress)
- `describe-snapshots` (by ID, verify VolumeId/Size/Description)
- `copy-snapshot` (with new description, verify distinct ID)
- `describe-snapshots` (verify both original + copy visible)
- `delete-snapshot` (original, verify gone while copy survives)
- `delete-snapshot` (copy, cleanup)

### Phase 5d: Verify Snapshot-Backed Instance Launch
- All `run-instances` calls use the snapshot path (`cloneAMIToVolume` -> `OpenFromSnapshot`), so the Phase 5 instance is already snapshot-backed
- Verify AMI snapshot exists in Predastore (`snap-{amiId}/config.json`)
- Read Phase 5 root volume's `config.json` from Predastore
- Verify `SnapshotID` and `SourceVolumeName` are set (proves zero-copy clone)

### Phase 5e: CreateImage Lifecycle
- `create-image` (from running instance, with name and description)
- Verify returned ImageId is non-empty
- `describe-images` (verify custom AMI name and state)

### Phase 6: Tag Management
- `create-tags` (3 tags on instance)
- `describe-tags` (filter by resource-id)
- `create-tags` (2 tags on volume)
- `describe-tags` (filter by key)
- `describe-tags` (filter by resource-type)
- `create-tags` (overwrite existing tag value)
- `delete-tags` (unconditional by key)
- `delete-tags` (with wrong value — should be no-op)
- `delete-tags` (with correct value)
- Verify final tag count

### Phase 7: Instance State Transitions
- `stop-instances` (poll -> stopped)

### Phase 7a: Attach Volume to Stopped Instance (Error Path)
- `create-volume` (for attach test)
- `attach-volume` to stopped instance (expect `IncorrectInstanceState` error)
- `delete-volume` (cleanup)

### Phase 7 (cont): Start and Restart
- `start-instances` (poll -> running)

### Phase 7b: RunInstances with count > 1
- `run-instances --count 2` (launch 2 instances in a single call)
- Verify 2 instances returned in response
- Poll both to running state
- `terminate-instances` (both, poll -> terminated)

### Phase 8: Negative / Error Path Tests
- `run-instances` with malformed AMI ID (expect `InvalidAMIID.Malformed`)
- `run-instances` with invalid instance type (expect `InvalidInstanceType`)
- `attach-volume` on in-use volume (expect `VolumeInUse`)
- `detach-volume` on boot volume (expect `OperationNotPermitted`)
- `delete-snapshot` on non-existent snapshot (expect `InvalidSnapshot.NotFound`)
- Unsupported Action via raw HTTP (expect `InvalidAction` or error response)

### Phase 9: Terminate and Verify Cleanup
- `terminate-instances` (poll -> terminated)

### Phase 9a: Volume Cleanup Verification
- `describe-volumes` on root volume after termination
- Verify root volume is deleted (DeleteOnTermination)

---

## Multi-Node (`run-multinode-e2e.sh`)

### Phase 1: Environment Setup
- KVM support check
- Simulated network IPs (10.11.12.{1,2,3} on loopback)
- Create ramdisk mount

### Phase 2: Cluster Initialization
- `hive admin init` (leader node1)
- CA certificate trust
- Start node1 services
- `hive admin join` (node2, node3)
- Start node2 + node3 services

### Phase 3: Cluster Health Verification
- Verify NATS cluster (3 nodes)
- Verify Predastore cluster (3 nodes)
- Wait for gateway on node1
- `describe-regions` (gateway connectivity check)

### Phase 4: Image and Key Setup
- `describe-instance-types` (discover + select nano)
- `create-key-pair`
- `hive admin images import` (with node1 config paths)
- `describe-images` (verify AMI)

### Phase 4b: Multi-Node Key Pair Operations
- `import-key-pair` (multinode-test-key-2, from local RSA key)
- `describe-key-pairs` (verify both keys visible across cluster)
- `delete-key-pair` (multinode-test-key-2, verify deletion)

### Phase 5: Multi-Node Instance Tests

#### Test 1: Instance Distribution
- `run-instances` x3 (distribute across nodes)
- Poll all instances to running state
- Check instance distribution across nodes

#### Test 1b: Volume Lifecycle
- `create-volume` (10GB)
- `modify-volume` (resize to 20GB)
- `attach-volume` (to first instance)
- `detach-volume`
- `delete-volume`

#### Test 1c: Snapshot Lifecycle
- Uses root volume of first instance (snapshots require a mounted VB instance)
- `create-snapshot` (from attached root volume, with description)
- Verify create response fields (VolumeId, VolumeSize, State)
- `describe-snapshots` (by ID, verify fields)
- `copy-snapshot` (with new description)
- `describe-snapshots` (verify both exist)
- `delete-snapshot` (original, verify copy survives)
- `delete-snapshot` (copy, cleanup)

#### Test 1c-ii: Verify Snapshot-Backed Instance Launch
- All `run-instances` calls use the snapshot path (`cloneAMIToVolume` -> `OpenFromSnapshot`), so the Test 1 instances are already snapshot-backed
- Verify AMI snapshot exists in Predastore (`snap-{amiId}/config.json`)
- Read first instance's root volume `config.json` from Predastore
- Verify `SnapshotID` and `SourceVolumeName` are set (proves zero-copy clone)

#### Test 1d: Tag Management (Instances)
- `create-tags` (3 tags on instance)
- `describe-tags` (filter by resource-id)
- `describe-tags` (filter by key)
- `describe-tags` (filter by resource-type)
- `create-tags` (overwrite tag value)
- `delete-tags` (unconditional by key)
- `delete-tags` (wrong value — no-op)
- `delete-tags` (correct value)
- Verify final tag count

#### Test 1d-ii: Tag Management (Volumes)
- `create-tags` (2 tags on root volume)
- `describe-tags` (filter by resource-id, verify count)
- `describe-tags` (filter by resource-type=volume)
- `delete-tags` (both tags, verify cleanup)

#### Test 2: DescribeInstances Aggregation
- `describe-instances` (fan-out across all nodes, verify count)

#### Test 3: Cross-Node Operations
- `stop-instances` (poll -> stopped)
- `start-instances` (poll -> running)

#### Test 4: NATS Cluster Health (Post-Operations)
- Verify NATS cluster still healthy after all operations

### Cleanup
- `terminate-instances` (all 3 instances)
- Poll all to terminated state
- Stop all node services
- Remove simulated IPs
