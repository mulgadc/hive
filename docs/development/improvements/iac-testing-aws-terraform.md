# IaC Compatibility: Terraform/OpenTofu with AWS Provider

**Status: Planned**

## Summary

Enable infrastructure-as-code workflows using the standard Terraform/OpenTofu AWS provider (`hashicorp/aws`) pointed at MulgaOS/Hive endpoints. The goal is to run `tofu apply` against a Hive cluster and have it create real infrastructure — EC2 instances, EBS volumes, key pairs — using the same `.tf` files that work against AWS.

## Context / Problem Statement

Hive already implements 37 EC2 API actions (instances, volumes, snapshots, images, key pairs, tags, etc.), but the Terraform AWS provider requires several additional APIs during `plan` and `apply` — particularly VPC networking resources that Terraform treats as mandatory dependencies for EC2 instances.

The existing `scripts/iac/aws/main.tf` defines a full VPC + subnet + security group + instance stack, but `tofu apply` fails because the VPC/networking APIs aren't implemented. We need to either:

1. **Stub** the networking APIs (accept input, return valid-looking output, store state) so Terraform can proceed, or
2. **Implement** them with real backing logic

For the initial milestone, stubbing is acceptable — Hive VMs already get networking through QEMU user-mode or bridge networking at the daemon level, so the VPC/subnet/SG resources are metadata-only for now. The critical requirement is that Terraform can complete a full `plan` → `apply` → `destroy` cycle.

## Current API Coverage

### Fully Implemented (works with Terraform)

| Resource | Terraform Type | Hive API Actions |
|---|---|---|
| EC2 Instances | `aws_instance` | `RunInstances`, `DescribeInstances`, `StartInstances`, `StopInstances`, `TerminateInstances`, `DescribeInstanceTypes` |
| EBS Volumes | `aws_ebs_volume` | `CreateVolume`, `DescribeVolumes`, `DescribeVolumeStatus`, `ModifyVolume`, `DeleteVolume` |
| Volume Attachment | `aws_volume_attachment` | `AttachVolume`, `DetachVolume` |
| Key Pairs | `aws_key_pair` | `CreateKeyPair`, `ImportKeyPair`, `DescribeKeyPairs`, `DeleteKeyPair` |
| AMI/Images | `aws_ami`, data source | `DescribeImages`, `CreateImage` |
| Snapshots | `aws_ebs_snapshot` | `CreateSnapshot`, `DescribeSnapshots`, `DeleteSnapshot`, `CopySnapshot` |
| Tags | (all resources) | `CreateTags`, `DeleteTags`, `DescribeTags` |
| Regions/AZs | data sources | `DescribeRegions`, `DescribeAvailabilityZones` |
| Account | data source | `DescribeAccountAttributes` |
| Egress IGW | `aws_egress_only_internet_gateway` | `Create/Describe/DeleteEgressOnlyInternetGateway` |

### Missing — Required by Terraform AWS Provider

These APIs are called by Terraform during `plan`/`apply` for common infrastructure patterns. Each needs at minimum a stub that accepts input, generates an ID, stores state in NATS KV, and returns a valid response.

#### Priority 1: VPC Networking (blocks `tofu apply` on `main.tf`)

| Terraform Resource | Required API Actions | Complexity |
|---|---|---|
| `aws_vpc` | `CreateVpc`, `DescribeVpcs`, `DeleteVpc`, `ModifyVpcAttribute` | Medium — needs ID generation, CIDR storage, state tracking |
| `aws_subnet` | `CreateSubnet`, `DescribeSubnets`, `DeleteSubnet` | Medium — references VPC, needs AZ assignment |
| `aws_security_group` | `CreateSecurityGroup`, `DescribeSecurityGroups`, `DeleteSecurityGroup`, `AuthorizeSecurityGroupIngress`, `AuthorizeSecurityGroupEgress`, `RevokeSecurityGroupIngress`, `RevokeSecurityGroupEgress` | High — complex rule model, Terraform creates a default SG per VPC |
| `aws_internet_gateway` | `CreateInternetGateway`, `DescribeInternetGateways`, `DeleteInternetGateway`, `AttachInternetGateway`, `DetachInternetGateway` | Medium — references VPC |
| `aws_route_table` | `CreateRouteTable`, `DescribeRouteTables`, `DeleteRouteTable`, `CreateRoute`, `DeleteRoute` | Medium — references VPC, subnet associations |
| `aws_route_table_association` | `AssociateRouteTable`, `DisassociateRouteTable` | Low — links subnet to route table |

#### Priority 2: Provider Initialization (called during `tofu init`/`plan`)

| Action | Why Terraform Calls It | Stub Needed |
|---|---|---|
| `GetCallerIdentity` (STS) | Provider startup — validates credentials | Yes — return static account ID |
| `DescribeVpcAttribute` | After VPC creation — queries DNS settings | Yes — return defaults |
| `DescribeNetworkInterfaces` | Instance creation — checks ENI state | Yes — return empty |
| `DescribeInstanceAttribute` | After instance creation — checks attributes | Yes — return defaults |

#### Priority 3: Nice-to-Have

| Terraform Resource | Required API Actions | Notes |
|---|---|---|
| `aws_placement_group` | `CreatePlacementGroup`, `DescribePlacementGroups`, `DeletePlacementGroup` | Used in `main.tf` for spread strategy |
| `aws_eip` | `AllocateAddress`, `DescribeAddresses`, `ReleaseAddress`, `AssociateAddress` | Static IP assignment |
| `aws_network_interface` | `CreateNetworkInterface`, `DescribeNetworkInterfaces`, `DeleteNetworkInterface` | Advanced networking |

## Proposed Changes

### Phase 1: VPC Networking Stubs

Implement stub handlers for all Priority 1 + Priority 2 APIs. Each stub:

- Accepts the AWS SDK input struct
- Generates a resource ID (e.g., `vpc-<random>`, `subnet-<random>`)
- Stores the resource in NATS KV (bucket per resource type)
- Returns a valid AWS-compatible response
- Supports `Describe*` to list stored resources
- Supports `Delete*` to remove from KV

Architecture:
```
hive/gateway/ec2/vpc/          # CreateVpc, DescribeVpcs, DeleteVpc
hive/gateway/ec2/subnet/       # CreateSubnet, DescribeSubnets, DeleteSubnet
hive/gateway/ec2/sg/           # Security group CRUD + rule management
hive/gateway/ec2/igw/          # Internet gateway CRUD + attach/detach
hive/gateway/ec2/rtb/          # Route table CRUD + associations
hive/gateway/sts/              # GetCallerIdentity stub
```

Storage: NATS KV buckets — `vpc`, `subnet`, `security-group`, `internet-gateway`, `route-table`. Each entry keyed by resource ID, value is JSON-serialized resource state.

### Phase 2: Terraform Integration Testing

Update `scripts/iac/aws/main.tf` and add automated validation:

1. **Simplify `main.tf`** — start with a minimal config that exercises implemented features:
   - Key pair (works today)
   - VPC + subnet + security group (after Phase 1 stubs)
   - EC2 instance with EBS volume
   - Tags on all resources

2. **Add `scripts/iac/aws/test.sh`** — automated test script:
   ```bash
   tofu init
   tofu plan -out=plan.tfplan
   tofu apply plan.tfplan
   # Verify resources exist via AWS CLI
   tofu destroy -auto-approve
   ```

3. **Add to CI** — optional E2E stage that runs the Terraform test against a running Hive instance

### Phase 3: Real VPC Networking (Future)

Replace stubs with real implementations as Hive networking matures:
- Map VPCs to Linux bridges or network namespaces
- Map subnets to DHCP ranges on bridges
- Map security groups to nftables/iptables rules
- Map internet gateways to NAT/routing rules

## Files to Modify

### Phase 1: New Gateway Handlers

| File | Description |
|---|---|
| `hive/gateway/ec2/vpc/CreateVpc.go` | Create VPC stub — generate ID, store in NATS KV |
| `hive/gateway/ec2/vpc/DescribeVpcs.go` | List VPCs from NATS KV |
| `hive/gateway/ec2/vpc/DeleteVpc.go` | Remove VPC from NATS KV |
| `hive/gateway/ec2/subnet/CreateSubnet.go` | Create subnet stub |
| `hive/gateway/ec2/subnet/DescribeSubnets.go` | List subnets |
| `hive/gateway/ec2/subnet/DeleteSubnet.go` | Delete subnet |
| `hive/gateway/ec2/sg/CreateSecurityGroup.go` | Create security group |
| `hive/gateway/ec2/sg/DescribeSecurityGroups.go` | List security groups |
| `hive/gateway/ec2/sg/DeleteSecurityGroup.go` | Delete security group |
| `hive/gateway/ec2/sg/AuthorizeIngress.go` | Add ingress rule |
| `hive/gateway/ec2/sg/AuthorizeEgress.go` | Add egress rule |
| `hive/gateway/ec2/sg/RevokeIngress.go` | Remove ingress rule |
| `hive/gateway/ec2/sg/RevokeEgress.go` | Remove egress rule |
| `hive/gateway/ec2/igw/CreateInternetGateway.go` | Create IGW stub |
| `hive/gateway/ec2/igw/DescribeInternetGateways.go` | List IGWs |
| `hive/gateway/ec2/igw/DeleteInternetGateway.go` | Delete IGW |
| `hive/gateway/ec2/igw/AttachInternetGateway.go` | Attach IGW to VPC |
| `hive/gateway/ec2/igw/DetachInternetGateway.go` | Detach IGW from VPC |
| `hive/gateway/ec2/rtb/CreateRouteTable.go` | Create route table |
| `hive/gateway/ec2/rtb/DescribeRouteTables.go` | List route tables |
| `hive/gateway/ec2/rtb/DeleteRouteTable.go` | Delete route table |
| `hive/gateway/ec2/rtb/CreateRoute.go` | Add route to table |
| `hive/gateway/ec2/rtb/DeleteRoute.go` | Remove route from table |
| `hive/gateway/ec2/rtb/AssociateRouteTable.go` | Associate subnet with route table |
| `hive/gateway/ec2/rtb/DisassociateRouteTable.go` | Disassociate route table |
| `hive/gateway/sts/GetCallerIdentity.go` | STS stub — return static account |

### Existing Files to Modify

| File | Description |
|---|---|
| `hive/gateway/ec2.go` | Register new VPC/subnet/SG/IGW/RTB actions in `ec2Actions` map |
| `hive/gateway/gateway.go` | Add STS service routing if needed |
| `scripts/iac/aws/main.tf` | Update endpoint config, adjust resources for current capabilities |
| `scripts/iac/aws/README.md` | Update with current API support status |

## Terraform AWS Provider Behavior Notes

Key things to know about how the Terraform AWS provider interacts with APIs:

1. **Provider init** calls `sts:GetCallerIdentity` unless `skip_requesting_account_id = true` — we set this in the provider config
2. **VPC creation** triggers `CreateVpc` then immediately `DescribeVpcs` to wait for `available` state, then `DescribeVpcAttribute` for DNS settings
3. **Security group** — Terraform creates a default SG when creating a VPC, and `CreateSecurityGroup` must return a `groupId`; rules are added via separate `AuthorizeSecurityGroup{Ingress,Egress}` calls
4. **Instance creation** — Terraform passes `SubnetId` and `SecurityGroupId` in `RunInstances`; we need to accept these without erroring even if networking is stubbed
5. **Destroy order** — Terraform destroys in reverse dependency order (instances first, then SGs, subnets, IGW, VPC). Each `Delete*` must succeed or Terraform retries.
6. **State refresh** — On every `plan`/`apply`, Terraform calls `Describe*` for every resource in state to detect drift. All `Describe*` endpoints must return consistent data.

## Testing

### Manual Validation

```bash
cd scripts/iac/aws
export TF_VAR_mulgaos_endpoint="https://localhost:9999"
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test

tofu init
tofu plan        # Should complete without errors
tofu apply       # Should create all resources
tofu destroy     # Should clean up all resources
```

### Automated (Future)

Add a Terraform E2E stage to CI that runs the full `init → plan → apply → verify → destroy` cycle against a Hive instance in Docker.

## Implementation Order

1. VPC + DescribeVpcs + DeleteVpc (unblocks subnet, SG, IGW)
2. Subnet + DescribeSubnets + DeleteSubnet
3. Security Group (full CRUD + rules)
4. Internet Gateway (CRUD + attach/detach)
5. Route Table (CRUD + routes + associations)
6. Wire all into `ec2.go` action map
7. Test with `tofu plan` (catches missing APIs)
8. Test with `tofu apply` (catches response format issues)
9. Update `scripts/iac/aws/main.tf` and README

## Future Work

- Replace VPC stubs with real Linux bridge/namespace networking
- Map security groups to nftables rules on hypervisor
- Implement Elastic IPs for static addressing
- Add S3 endpoint support (Predastore already S3-compatible)
- Implement IAM basics for multi-tenant access control
- Add CloudWatch-compatible metrics endpoint
