# E2E Test Coverage

## Single-Node (`run-e2e.sh`)

### Phase 1: Environment Setup
- KVM support check
- `spx admin init` (region/az/node config)
- CA certificate trust
- Start all services (`start-dev.sh`)
- Wait for AWS gateway on `localhost:9999`

### Phase 1b: Cluster Stats CLI
- `spx get nodes` — verify node shows Ready
- `spx top nodes` — verify CPU/MEM resource stats
- `spx get vms` — verify "No VMs found" before any launches

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
- `spx admin images import` (file-based, architecture-aware)
- `describe-images` (verify AMI by ID)

### Phase 5: Instance Lifecycle
- `run-instances` (launch VM with key pair)
- `describe-instances` (poll pending -> running)

### Phase 5a-pre: Cluster Stats CLI (with running VM)
- `spx get vms` — verify running instance appears in output

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

### IAM Phase 1: User CRUD
- Root auth via `iam list-users` (root user exists)
- `create-user` (alice, bob with path)
- Duplicate user (expect `EntityAlreadyExists`)
- `get-user` (alice)
- `get-user` non-existent (expect `NoSuchEntity`)
- `list-users` (verify count)
- `list-users` with `--path-prefix` filter

### IAM Phase 2: Access Key Lifecycle
- `create-access-key` (alice key 1, key 2)
- Third key exceeds limit (expect `LimitExceeded`)
- `create-access-key` for non-existent user (expect `NoSuchEntity`)
- `list-access-keys` (alice: 2 keys, bob: 0 keys)
- `update-access-key` (Inactive, verify status, reactivate)
- `delete-access-key` (verify count decremented)

### IAM Phase 3: User Authentication
- Configure IAM user profile with access key
- Deactivate key → auth rejected (`InvalidClientTokenId`)
- Reactivate key
- Wrong secret → `SignatureDoesNotMatch`
- Non-existent key ID → `InvalidClientTokenId`
- Multi-user simultaneous auth (root + bob)

### IAM Phase 4: Policy CRUD
- `create-policy` (EC2ReadOnly, FullAdmin, DenyTerminate, IAMReadOnly, EC2DescribeAll)
- Duplicate policy (expect `EntityAlreadyExists`)
- Malformed JSON (expect `MalformedPolicyDocument`)
- `get-policy` (by ARN)
- `get-policy` non-existent (expect `NoSuchEntity`)
- `get-policy-version` (v1)
- `list-policies` (verify count)

### IAM Phase 5: Policy Attachment & Enforcement
- Create charlie with access key
- `attach-user-policy` (alice: EC2ReadOnly + IAMReadOnly, bob: DenyTerminate)
- `list-attached-user-policies` (verify count)
- Idempotent attach (no duplicate)
- Attach non-existent policy (expect `NoSuchEntity`)
- Attach to non-existent user (expect `NoSuchEntity`)
- **Default Deny**: charlie (no policies) → `AccessDenied` on ec2 + iam
- **Explicit Allow**: alice → ec2:Describe{Instances,Vpcs} allowed, ec2:DescribeKeyPairs denied, iam:ListUsers allowed, iam:CreateUser denied
- **Deny Override**: bob → ec2:Describe allowed, ec2:TerminateInstances denied (explicit Deny), iam denied
- **Root Bypass**: root user → all actions succeed
- **Prefix Wildcard**: swap alice to EC2DescribeAll (ec2:Describe*) → Describe* allowed, non-Describe denied
- **FullAdmin**: charlie with FullAdmin → all actions allowed

### IAM Phase 6: Policy Lifecycle — Detach & Delete
- `detach-user-policy` → user loses access
- `delete-policy` while attached (expect `DeleteConflict`)
- Detach then delete → `get-policy` returns `NoSuchEntity`

### IAM Phase 7: IAM Cleanup
- Delete all test users (remove keys + policies first)
- Verify root-only remains (`list-users` count = 1)
- Delete all test policies (`list-policies` count = 0)
- Clean up AWS CLI profiles

### Phase 8: EC2 Account Scoping

Tests that EC2 resources are properly isolated between tenant accounts (Alpha, Beta). Based on `docs/development/feature/iam-phase4-e2e-test-guide.md`. Skips CreateImage (mulga-612) and instance tags (mulga-613).

#### Step 1: Account Setup
- `spx admin account create` (Team Alpha, Team Beta)
- Configure AWS CLI profiles
- Verify auth for both accounts

#### Step 2: Instance Scoping
- Alpha + Beta each launch 1 instance
- `describe-instances` isolation (each sees only own)
- OwnerId field matches account ID
- Cross-account `stop-instances` → `InvalidInstanceID.NotFound`
- Cross-account `terminate-instances` → `InvalidInstanceID.NotFound`
- Cross-account `start-instances` → `InvalidInstanceID.NotFound`
- Cross-account `modify-instance-attribute` → `InvalidInstanceID.NotFound`
- Cross-account `get-console-output` → `InvalidInstanceID.NotFound`

#### Step 3: Volume Scoping
- Alpha + Beta each create 1 volume
- `describe-volumes` isolation
- Cross-account `describe-volumes` by ID → `InvalidVolume.NotFound`
- Cross-account `delete-volume` → `InvalidVolume.NotFound`
- Cross-account `attach-volume` → `InvalidVolume.NotFound`
- Cross-account `detach-volume` → `InvalidVolume.NotFound`
- Cross-account `modify-volume` → `InvalidVolume.NotFound`

#### Step 4: Key Pair Scoping
- Alpha + Beta each create key pair
- `describe-key-pairs` isolation
- Same key name in both accounts (namespace isolation — different KeyPairIds)
- Cross-account `delete-key-pair` → no effect on other account's key
- `import-key-pair` → invisible to other account

#### Step 5: Snapshot Scoping
- Alpha + Beta each create snapshot from own volume
- `describe-snapshots` isolation + OwnerId verification
- Cross-account `delete-snapshot` → `UnauthorizedOperation`
- Cross-account `create-snapshot` from other's volume → `InvalidVolume.NotFound`

#### Step 6: VPC/Subnet Scoping
- Alpha + Beta each create VPC (same CIDR — no conflict)
- `describe-vpcs` isolation
- Cross-account `describe-vpcs` by ID → `InvalidVpcID.NotFound`
- Cross-account `delete-vpc` → `InvalidVpcID.NotFound`
- Alpha + Beta each create subnet
- `describe-subnets` isolation
- Cross-account `create-subnet` in other's VPC → `InvalidVpcID.NotFound`
- Cross-account `delete-subnet` → `InvalidSubnetID.NotFound`

#### Step 7: IGW + EIGW Scoping
- Alpha + Beta each create IGW
- `describe-internet-gateways` isolation
- Cross-account `describe-internet-gateways` by ID → `InvalidInternetGatewayID.NotFound`
- Cross-account `delete-internet-gateway` → `InvalidInternetGatewayID.NotFound`
- Cross-account `attach-internet-gateway` → `InvalidInternetGatewayID.NotFound`
- Cross-account `detach-internet-gateway` → `InvalidInternetGatewayID.NotFound`
- Alpha + Beta each create EIGW
- `describe-egress-only-internet-gateways` isolation
- Cross-account EIGW delete → no effect

#### Step 8: Account Settings
- Alpha `enable-ebs-encryption-by-default` → Beta unaffected
- Independent toggle verification

#### Step 9: Global Resources
- `describe-regions` identical for both accounts
- `describe-availability-zones` identical
- `describe-instance-types` identical

#### Step 10: Edge Cases
- Empty account (Gamma) — no resources visible
- Root isolation from tenants (root cannot see tenant instances)
- Non-existent resource IDs → same error as cross-account (no info leakage)

#### Step 11: Cleanup
- Terminate instances, delete volumes/snapshots/keys/VPCs/IGWs/EIGWs/subnets
- Clean up AWS CLI profiles

### Phase 9: Terminate and Verify Cleanup
- `delete-snapshot` (CreateImage backing snapshot, so DeleteOnTermination is not blocked)
- `terminate-instances` (poll -> terminated)

### Phase 9a: SSH Unreachable Verification
- Verify SSH connection is refused/unreachable after termination

### Phase 9b: Volume Cleanup Verification
- `describe-volumes` on root volume after termination
- Verify root volume is deleted (DeleteOnTermination)

---

## Pseudo Multi-Node (`run-pseudo-multinode-e2e.sh`)

### Phase 1: Environment Setup
- KVM support check
- Simulated network IPs (10.11.12.{1,2,3} on loopback)
- Simulated network IPs (no ramdisk — start-dev.sh uses disk-backed WAL/VB in CI)

### Phase 2: Cluster Initialization
- `spx admin init` (leader node1)
- CA certificate trust
- Start node1 services
- `spx admin join` (node2, node3)
- Start node2 + node3 services

### Phase 3: Cluster Health Verification
- Verify NATS cluster (3 nodes)
- Verify Predastore cluster (3 nodes)
- Wait for gateway on node1
- Wait for daemon NATS readiness
- `describe-regions` (gateway connectivity check)

### Phase 3b: Cluster Stats CLI
- `spx get nodes` — verify all 3 nodes show Ready
- `spx top nodes` — verify instance type capacity table
- `spx get vms` — verify empty (no instances yet)

### Phase 4: Image and Key Setup
- `describe-instance-types` (discover + select nano)
- `create-key-pair`
- `spx admin images import` (with node1 config paths)
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
- `spx get vms` — verify all instances visible

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

### Phase 5c: IAM Accounts & Cross-Account Isolation

#### Step 1: Create Accounts
- `spx admin account create` (Team Alpha, Team Beta)
- Verify sequential 12-digit account IDs
- `spx admin account list` (verify both accounts)

#### Step 2: Account Admin Auth
- Alpha admin authenticates via ec2 + iam
- Beta admin authenticates via ec2 + iam

#### Step 3: Account-Scoped Users
- `create-user` alice in Alpha + Beta (same name, different accounts)
- `create-user` team-member (Alpha), dev-user (Beta)
- `list-users` scoped per account (verify different user lists)
- Cross-account isolation: Alpha cannot see Beta's users and vice versa (`NoSuchEntity`)

#### Step 4: Account-Scoped Access Keys
- `create-access-key` for alice in Alpha + Beta
- Configure separate AWS CLI profiles per account user

#### Step 5: Account-Scoped Policies & Enforcement
- `create-policy` EC2ReadOnly in Alpha (narrow: DescribeInstances + DescribeVpcs)
- `create-policy` EC2ReadOnly in Beta (broad: ec2:Describe*)
- `attach-user-policy` to alice in both accounts
- Alpha alice: DescribeInstances allowed, DescribeKeyPairs denied (narrow)
- Beta alice: DescribeInstances allowed, DescribeKeyPairs allowed (broad Describe*)
- Both denied: CreateKeyPair

#### Step 6: Cross-Account Delete Isolation
- Delete Alpha's alice (detach policy, delete key, delete user)
- Verify Alpha alice gone (`NoSuchEntity`)
- Verify Beta alice unaffected (still exists, auth still works)

#### Step 7: EC2 Resource Scoping

Tests that EC2 resources are properly isolated between the Alpha/Beta accounts. Skips CreateImage (mulga-612) and instance tags (mulga-613).

**7a: Instance Scoping**
- Alpha + Beta each launch 1 instance
- `describe-instances` isolation (each sees only own)
- OwnerId field matches account ID
- Cross-account `stop-instances` → `InvalidInstanceID.NotFound`
- Cross-account `terminate-instances` → `InvalidInstanceID.NotFound`
- Cross-account `get-console-output` → `InvalidInstanceID.NotFound`

**7b: Volume Scoping**
- Alpha + Beta each create 1 volume
- `describe-volumes` isolation
- Cross-account `describe-volumes` by ID → `InvalidVolume.NotFound`
- Cross-account `delete-volume` → `InvalidVolume.NotFound`
- Cross-account `attach-volume` → `InvalidVolume.NotFound`
- Cross-account `detach-volume` → `InvalidVolume.NotFound`
- Cross-account `modify-volume` → `InvalidVolume.NotFound`

**7c: Key Pair Scoping**
- Alpha + Beta each create key pair
- `describe-key-pairs` isolation
- Same key name in both accounts (namespace isolation — different KeyPairIds)
- Cross-account `delete-key-pair` → no effect on other account's key
- `import-key-pair` → invisible to other account

**7d: Snapshot Scoping**
- Alpha + Beta each create snapshot from own volume
- `describe-snapshots` isolation + OwnerId verification
- Cross-account `delete-snapshot` → `UnauthorizedOperation`
- Cross-account `create-snapshot` from other's volume → `InvalidVolume.NotFound`

**7e: VPC/Subnet Scoping**
- Alpha + Beta each create VPC (same CIDR — no conflict)
- `describe-vpcs` / `describe-subnets` isolation
- Cross-account VPC/subnet operations → `NotFound`

**7f: IGW + EIGW Scoping**
- Alpha + Beta each create IGW + EIGW
- Describe isolation for both
- Cross-account attach/detach/delete → `NotFound`

**7g: Account Settings**
- `enable-ebs-encryption-by-default` per account — independent toggle

**7h: Global Resources**
- `describe-regions`, `describe-availability-zones`, `describe-instance-types` identical for both accounts

#### Step 8: Edge Cases
- Empty account (Gamma) — no resources visible
- Root isolation from tenants
- Non-existent resource IDs → same error as cross-account
- Parallel key creation (race condition) — no cross-contamination

#### Step 9: EC2 + IAM Cleanup
- Terminate instances, delete volumes/snapshots/keys/VPCs/IGWs/EIGWs/subnets
- Delete all IAM users, policies, access keys
- Clean up AWS CLI profiles

### Phase 5d: VPC Networking
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
- `spx admin cluster shutdown --dry-run`
- Validate output contains all 5 phases (GATE, DRAIN, STORAGE, PERSIST, INFRA)

#### Test 6b: Coordinated Cluster Shutdown
- `spx admin cluster shutdown --force --timeout 60s`
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

## Real Multi-Node (`run-multinode-e2e.sh`)

Runs on a real 3-node libvirt cluster provisioned by OpenTofu (`scripts/tofu-cluster/`). Each node is a separate VM with its own OVN, NATS, Predastore, and Spinifex daemon. Bootstrap (`bootstrap.sh`) handles all provisioning before the test script runs.

### Bootstrap (pre-test)

1. OpenTofu provisions 3 libvirt VMs (bottlebrush, ironbark, casuarina) with cloud-init
2. Wait for all nodes to be SSH-reachable
3. Copy SSH key to node1 for peer access
4. `git fetch` + checkout test branch on all nodes (all 3 repos)
5. `make build` on all nodes (parallel)
6. `setup-ovn.sh` — primary gets `--management`, secondaries connect to primary's OVN central
7. `spx admin init` on primary, `spx admin join` on secondaries (formation server handshake)
8. `start-dev.sh` on all nodes
9. Wait for `/health` (awsgw=ok) on all nodes
10. Install Spinifex CA certificate on all nodes
11. `import-key-pair` (spinifex-key) + `spx admin images import` (Ubuntu 24.04)
12. `create-vpc` (10.200.0.0/16) + `create-subnet` (10.200.1.0/24)

### Phase 1: Pre-flight Validation
- KVM support check (`/dev/kvm` writable)
- SSH connectivity to all peer nodes

### Phase 2: Cluster Health
- NATS cluster: verify 2 unique route peers (3-node quorum)
- Predastore reachable on all 3 nodes
- AWS gateway reachable on all 3 nodes
- Daemon readiness (`describe-instance-types` returns results)
- `spx get nodes` — verify all 3 nodes show Ready
- `spx get vms` — verify empty (no instances yet)

### Phase 3: Instance Lifecycle + Distribution
- Discover nano instance type and AMI (from bootstrap import)
- `run-instances` x3 with staggered launches
- Poll all instances to running state
- Check instance distribution across physical nodes (QEMU process check)
- Verify at least 2 different hosting nodes (non-deterministic, non-fatal if all on 1)
- `spx get vms` — verify all 3 instances visible

### Phase 4: SSH into Guest VMs
- For each instance across all nodes:
  - Find hosting node via QEMU process scan
  - Extract SSH hostfwd port from QEMU command line on remote node
  - Wait for SSH readiness (up to 60s)
  - Verify SSH connectivity (`id` returns ec2-user)
  - Verify block device (`lsblk`)

### Phase 5: Volume Lifecycle
- `create-volume` (10GB)
- `attach-volume` (to first instance, /dev/sdf)
- Poll for in-use + attached state
- `detach-volume` (poll for available)
- `delete-volume` (verify gone)

### Phase 6: Cross-Node Gateway Access
- `describe-instances` via node1 gateway (baseline count)
- `describe-instances` via node2 and node3 gateways
- Verify all gateways return the same instance count (fan-out aggregation)

### Phase 7: Cross-Node Operations
- Find which node hosts instance 0
- Pick a different node's gateway for stop
- `stop-instances` via remote gateway (poll -> stopped)
- Pick a third node's gateway for start
- `start-instances` via another remote gateway (poll -> running)

### Phase 8: Node Failure
- `stop-dev.sh` on node2 (simulate node failure)
- Verify node1 still serves requests (`describe-instance-types`)
- Verify node3 still serves requests
- Check NATS degraded state (1 peer instead of 2)
- `describe-instances` still works from surviving nodes

### Phase 9: Node Recovery
- `start-dev.sh` on node2 (restart failed node)
- Wait for NATS cluster to reform (2 peers again)
- Verify node2 gateway is back
- `spx get nodes` — verify all 3 nodes Ready again
- Verify node2 serves requests after recovery

### Phase 10: Cleanup
- `terminate-instances` (all 3 instances, poll -> terminated)

---

## VPC (`run-vpc-e2e.sh`)

Standalone VPC networking test suite. Runs against a running Spinifex endpoint (configurable via `ENDPOINT` env var). OVN topology tests are skipped when OVN is unavailable.

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

---

## ELBv2 / ALB (`run-elbv2-e2e.sh`)

Standalone ELBv2 (Application Load Balancer) E2E test suite. Requires VPC networking
for ALB ENI creation. Configurable endpoint via `ENDPOINT` env var.

### Phase 0: VPC + Subnet Setup
- `create-vpc` (10.200.0.0/16, prerequisite for ALB)
- `create-subnet` (10.200.1.0/24)

### Phase 1: Target Group CRUD
- `create-target-group` (HTTP, port 80, verify health check defaults)
- `describe-target-groups` (by ARN, verify single result)
- `create-target-group` (second TG, port 8080)
- `describe-target-groups` (all, verify >= 2)
- Duplicate name detection (same name → error)

### Phase 2: Target Registration
- `register-targets` (2 fake instances)
- `describe-target-health` (verify 2 targets, initial state)
- `deregister-targets` (remove 1 target)
- `describe-target-health` (verify 1 target remains)

### Phase 3: Load Balancer CRUD
- `create-load-balancer` (ALB with subnet)
- Verify fields: type=application, state=active, DNS name, scheme=internet-facing
- Verify ENIs created (describe-network-interfaces with ELB filter)
- `describe-load-balancers` (by ARN, verify single result)
- Duplicate name detection (same name → error)

### Phase 4: Listener CRUD
- `create-listener` (port 80, forward to TG)
- Verify fields: port=80, protocol=HTTP
- `describe-listeners` (by LB ARN, verify single result)
- Duplicate port detection (same port → error)

### Phase 5: In-Use Protection
- `delete-target-group` while referenced by listener → error (ResourceInUse)

### Phase 6: Listener Deletion
- `delete-listener`
- Verify 0 listeners remain
- `delete-target-group` (now succeeds after listener removed)

### Phase 7: Load Balancer Deletion
- `delete-load-balancer`
- Verify ALB gone from describe
- Verify ENIs cleaned up
- `delete-target-group` (second TG)

### Phase 8: Error Path Tests
- Describe non-existent ALB → empty result
- Delete non-existent ALB → error
- Delete non-existent TG → error
- Create TG without name → error
- Create listener on non-existent ALB → error

---

## ELBv2 / ALB Data Plane (`run-elbv2-dataplane-e2e.sh`)

Data plane test that verifies ALBs actually balance HTTP traffic across instances.
Requires VPC networking (OVN), HAProxy on daemon node, and imported AMI. Designed
for pseudo-multinode or real multi-node environments.

### Phase 0: Prerequisites
- Discover nano instance type
- Discover AMI (must be pre-imported)

### Phase 1: VPC + Subnet Setup
- `create-vpc` (10.201.0.0/16)
- `create-subnet` (10.201.1.0/24)

### Phase 2: Launch Instances
- `run-instances` x2 with cloud-init HTTP responder (Python http.server on port 80)
- Cloud-init serves `{"instance_id": "<hostname>"}` on each instance
- Poll both instances to running state
- Verify both have `PrivateIpAddress` assigned

### Phase 3: Target Group + Registration
- `create-target-group` (HTTP, port 80, health check on /index.html, 5s interval)
- `register-targets` (both instances)

### Phase 4: ALB + Listener
- `create-load-balancer` (ALB with subnet)
- Resolve ALB ENI private IP (via describe-network-interfaces)
- `create-listener` (port 80, forward to target group)

### Phase 5: Wait for Target Health
- Poll `describe-target-health` until both targets healthy (120s timeout)
- Continue even if targets stay in "initial" (HAProxy may still forward)

### Phase 6: Traffic Balancing — Round Robin
- `curl` ALB ENI IP 20 times, parse `instance_id` from JSON response
- Verify responses contain BOTH instance IDs (round-robin distribution)
- Verify success rate >= 50%

### Phase 7: Single Target After Deregister
- `deregister-targets` (remove second instance)
- Wait for HAProxy reload (3s)
- `curl` ALB 20 times
- Verify ONLY the remaining instance responds
- Verify success rate >= 50%

### Phase 8: Re-register + Recovery
- `register-targets` (re-add deregistered instance)
- Wait for HAProxy reload (5s)
- `curl` ALB 20 times
- Verify BOTH instances responding again
