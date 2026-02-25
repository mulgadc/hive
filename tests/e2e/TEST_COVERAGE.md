# E2E Test Coverage

## Single-Node (`run-e2e.sh`)

### Phase 1: Environment Setup
- KVM support check
- `hive admin init` (region/az/node config)
- CA certificate trust
- Start all services (`start-dev.sh`)
- Wait for AWS gateway on `localhost:9999`

### Phase 1b: Cluster Stats CLI
- `hive get nodes` — verify node shows Ready
- `hive top nodes` — verify CPU/MEM resource stats
- `hive get vms` — verify "No VMs found" before any launches

### Phase 2: Discovery & Metadata
- `describe-regions`
- `describe-availability-zones` (verify zone name and state)
- `describe-instance-types` (discover available types)
- Select nano instance type and detect architecture

### Phase 2b: Serial Console Access Settings
- `get-serial-console-access-status` (verify default disabled)
- `enable-serial-console-access` (verify returns true, get confirms true)
- `disable-serial-console-access` (verify returns false, get confirms false)

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

### Phase 5a-pre: Cluster Stats CLI (with running VM)
- `hive get vms` — verify running instance appears in output

### Phase 5a: Instance Metadata Validation
- `describe-instances` — verify InstanceType matches requested type
- Verify KeyName matches requested key
- Verify ImageId matches requested AMI
- Verify at least 1 BlockDeviceMapping present

### Phase 5a-ii: SSH Connectivity & Volume Verification
- SSH into instance via QEMU hostfwd port
- Verify SSH connectivity (`id` command returns ec2-user)
- Verify root volume size from inside VM matches API-reported size (`lsblk` vs `describe-volumes`)
- Verify VM hostname

### Phase 5a-iii: Console Output
- `get-console-output` succeeds (verify InstanceId in response)

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
- Extract backing snapshot ID from Predastore config (for cleanup before termination)

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

### Phase 7b: ModifyInstanceAttribute
- `modify-instance-attribute` (change instance type from nano → xlarge while stopped)
- `describe-instances` (verify type updated in KV)
- `describe-instance-types` (get expected vCPU count and memory for new type)
- `start-instances` (poll -> running with new type)
- SSH: `nproc` — verify vCPU count matches xlarge (4 vCPUs)
- SSH: `/proc/meminfo` MemTotal — verify memory matches xlarge (within 85% of expected)

### Phase 7c: RunInstances with count > 1
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
- `run-instances` with non-existent AMI ID (expect `InvalidAMIID.NotFound`)
- `run-instances` with non-existent key pair (expect `InvalidKeyPair.NotFound`)
- `delete-volume` on non-existent volume (expect `InvalidVolume.NotFound`)
- `create-key-pair` with duplicate name (expect `InvalidKeyPair.Duplicate`)
- `import-key-pair` with duplicate name (expect `InvalidKeyPair.Duplicate`)
- `import-key-pair` with invalid key format (expect `InvalidKey.Format`)
- `describe-volumes` with non-existent volume ID (expect `InvalidVolume.NotFound`)
- `describe-images` with non-existent AMI ID (expect `InvalidAMIID.NotFound`)
- `create-image` with duplicate name (expect `InvalidAMIName.Duplicate`)
- `delete-key-pair` on non-existent key (expect success — idempotent, matches AWS)
- `modify-instance-attribute` on running instance (expect `InvalidInstanceID.NotFound` — running instances not in stopped KV)

### Phase 9: Terminate and Verify Cleanup
- `delete-snapshot` (CreateImage backing snapshot, so DeleteOnTermination is not blocked)
- `terminate-instances` (poll -> terminated)

### Phase 9a: SSH Unreachable Verification
- Verify SSH connection is refused/unreachable after termination

### Phase 9b: Volume Cleanup Verification
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
- Wait for daemon NATS readiness
- `describe-regions` (gateway connectivity check)

### Phase 3b: Cluster Stats CLI
- `hive get nodes` — verify all 3 nodes show Ready
- `hive top nodes` — verify instance type capacity table
- `hive get vms` — verify empty (no instances yet)

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
- `hive get vms` — verify all instances visible

#### Test 1a-ii: SSH Connectivity & Volume Verification
- SSH into all 3 instances via QEMU hostfwd port
- Verify SSH connectivity (`id` command returns ec2-user)
- Verify root volume size from inside VM matches API-reported size (`lsblk` vs `describe-volumes`)
- Verify VM hostname

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

#### Test 5: VM Crash Recovery
- Kill QEMU process with SIGKILL (simulate OOM kill)
- Verify daemon detects crash (instance transitions to error/pending)
- Wait for auto-restart (backoff starts at 5s)
- Verify new QEMU PID differs from original
- Verify instance reaches running state
- Verify SSH connectivity after recovery

#### Test 5b: Crash Loop Prevention
- Kill QEMU 4 times rapidly on a third instance
- Verify crash loop is detected and restarts stop after max attempts (3 in 10-min window)
- Verify instance reaches error state (won't restart further)

### Phase 5c: VPC Networking
- Step 1: `create-vpc` (10.100.0.0/16) + `create-subnet` (10.100.1.0/24)
- Step 2: `run-instances` x3 with `--subnet-id` (launch into VPC subnet)
- Poll all VPC instances to running state
- Step 3: Verify `PrivateIpAddress` in `describe-instances` for each instance
- Verify `SubnetId` and `VpcId` match requested values
- Verify at least 1 `NetworkInterface` per instance
- Verify all IPs are unique and in subnet range (10.100.1.x)
- Step 4: SSH + ping connectivity (skipped in CI — OVN DHCP wait too slow)
- Step 5: Stop/start IP persistence
  - `stop-instances` (all VPC instances, poll -> stopped)
  - Verify `PrivateIpAddress` persists in stopped state
  - `start-instances` (all VPC instances, poll -> running)
  - Verify `PrivateIpAddress` identical after restart
- Step 6: Cleanup — terminate VPC instances, delete subnet, delete VPC

### Phase 6: Cluster Shutdown + Restart

#### Test 6a: Dry-Run Shutdown
- `hive admin cluster shutdown --dry-run`
- Validate output contains all 5 phases (GATE, DRAIN, STORAGE, PERSIST, INFRA)

#### Test 6b: Coordinated Cluster Shutdown
- `hive admin cluster shutdown --force --timeout 60s`
- Verify all services down on all nodes (gateway, NATS, QEMU)

#### Test 6c: Cluster Restart + Recovery
- Restart all 3 node services
- Verify NATS cluster reforms (3 members)
- Wait for gateway and daemon readiness
- Smoke test: `describe-instance-types` returns valid results

#### Test 6d: Instance Relaunch + Terminate
- Wait for instances to finish relaunching after restart (pending → running/error)
- `terminate-instances` (all 3 instances)
- Poll all to terminated state

### Cleanup
- Coordinated cluster shutdown (with fallback to per-node PID stops)
- Remove simulated IPs

---

## VPC (`run-vpc-e2e.sh`)

Standalone VPC networking test suite. Runs against a running Hive endpoint (configurable via `ENDPOINT` env var). OVN topology tests are skipped when OVN is unavailable.

### Phase 1: VPC CRUD
- `create-vpc` (10.99.0.0/16, verify VpcId)
- `describe-vpcs` (by ID, verify exactly 1 returned)

### Phase 2: Subnet CRUD
- `create-subnet` (10.99.1.0/24 in VPC, verify SubnetId)
- `describe-subnets` (by ID, verify exactly 1 returned)

### Phase 3: Internet Gateway CRUD
- `create-internet-gateway` (verify InternetGatewayId)
- `describe-internet-gateways` (by ID, verify exactly 1 returned)

### Phase 4: Internet Gateway Attach / Detach
- `attach-internet-gateway` (IGW to VPC)
- `describe-internet-gateways` (verify attachment VpcId)
- `delete-internet-gateway` on attached IGW (expect `DependencyViolation` or rejection)
- `detach-internet-gateway` (IGW from VPC)
- `describe-internet-gateways` (verify no attachments)

### Phase 5: OVN Topology Verification (requires OVN)
- Re-attach IGW for topology inspection
- Verify OVN logical switch exists for subnet
- Verify OVN logical router exists for VPC
- Verify OVN external switch exists (IGW attached)
- Verify SNAT rule on VPC router
- Verify default route (0.0.0.0/0) on VPC router
- Dump full OVN NB DB topology for debugging
- Detach IGW
- Skipped when `ovn-nbctl` is not available

### Phase 6: Cleanup
- `delete-internet-gateway`
- `delete-subnet`
- `delete-vpc`
- Verify OVN cleanup (no VPC routers remaining, when OVN available)
