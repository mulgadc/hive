# Reusable IaC Test Environments for Hive

## Summary

Make the Proxmox IaC in `scripts/iac/proxmox/` reusable and parameterized so that multiple isolated Hive clusters can be provisioned, configured, tested, and destroyed independently. Introduce a four-phase lifecycle (Provision, Configure, Test, Destroy) with a single wrapper script, inspired by Kubernetes kubetest2, QEMU avocado, and SUSE Rancher patterns.

**Status: Planned**

## Context / Problem Statement

The current Proxmox IaC works well for its original purpose — provisioning the 3 static dev servers (10.1.3.170-172). But it has several limitations that prevent reuse:

### What exists today

| File | Purpose |
|---|---|
| `scripts/iac/proxmox/main.tf` | Provider config (`bpg/proxmox` v0.52.0), locals for node map |
| `scripts/iac/proxmox/vms.tf` | VM resources, hardcoded 4 vCPU / 8GB RAM / 32GB disk, output `iac_mulgaos_ips` |
| `scripts/iac/proxmox/variables.tf` | Input vars: `proxmox_endpoint`, `ssh_*_key_path`, `nodes` (exactly 3) |
| `scripts/iac/proxmox/cloud-config.tf` | Cloud-init: installs packages, clones hive, runs `quickinstall` + `dev-setup.sh` |
| `scripts/iac/proxmox/README.md` | Manual cluster setup instructions (init node 1, join nodes 2-3) |

### Current gaps

1. **Hardcoded to 3 nodes** — `variables.tf` validates `length(var.nodes) == 3`, can't run a 1-node smoke test or a 5-node stress test
2. **Single state file** — `terraform.tfstate` lives in the working directory, no way to have parallel environments without manually copying the directory (which is what `proxmox.orig/` is doing today)
3. **Hardcoded VM names** — `iac-dev-mulgaos-${index}` means two environments would collide on VM names
4. **Hardcoded specs** — 4 vCPU, 8GB RAM, 32GB disk baked into `vms.tf`, no way to test with different sizes
5. **Cloud-init does too much** — clones hive from GitHub, runs `quickinstall`, runs `dev-setup.sh`. This couples provisioning to a specific hive version and makes iteration slow (full cloud-init reruns on every `tofu apply`)
6. **No automated post-provisioning** — after `tofu apply`, the README has manual SSH commands for `hive admin init` on node 1, `hive admin join` on nodes 2-3, then manual `hive start` on each
7. **No test orchestration** — no way to automatically run E2E tests against a provisioned cluster

## Design Principles

Drawing from how other infrastructure projects handle automated test environments:

### Kubernetes kubetest2

Pluggable deployer interface with lifecycle phases: `up -> test -> down`. Deployers are swappable (kind, GKE, EKS) but the lifecycle contract is the same. The test harness is decoupled from infrastructure creation — tests don't know or care whether they're running against kind or GKE.

**Takeaway**: Separate the lifecycle wrapper from the infrastructure backend. Today we have Proxmox; tomorrow we might add AWS or libvirt. The `up/test/down` contract stays the same.

### QEMU avocado

Parameterized test matrices — vary CPU count, memory, disk type, architecture across test runs. Test cases are decoupled from environment parameters. A single test suite runs against many different configurations.

**Takeaway**: Make node count, CPU, memory, and disk size variables, not constants. Enable test matrix runs like "3-node/4-core vs 5-node/8-core".

### SUSE Rancher

Two-layer IaC — Layer 1 provisions VMs (bare OS), Layer 2 deploys software (Kubernetes/Rancher). Clean separation means you can re-deploy software without re-provisioning VMs, and vice versa.

**Takeaway**: Cloud-init should only set up the OS. Hive installation and cluster formation belong in a separate configure phase that can be re-run independently.

### Terraform best practices

Separate state per environment (workspaces are for identical configs in different accounts, not divergent environments). Variables for everything that changes. Outputs from one phase become inputs to the next.

**Takeaway**: Use `-state=clusters/{name}/terraform.tfstate` for state isolation. Write structured outputs (node IPs, SSH config) to `clusters/{name}/inventory.json` for the configure phase to consume.

## Proposed Changes

### Four-phase lifecycle

```
Provision (tofu)  ->  Configure (shell)  ->  Test (shell)  ->  Destroy (tofu)
     |                      |                     |                   |
     v                      v                     v                   v
  VMs exist            Hive running          Tests pass          VMs gone
  IPs known            Cluster formed        Results saved       State cleaned
```

### Phase 1: Provision

Terraform/OpenTofu creates VMs with a minimal OS. No hive-specific setup.

**Changes to `scripts/iac/proxmox/variables.tf`:**

```hcl
variable "cluster_name" {
  type        = string
  description = "Unique name for this cluster (used in VM names, state path, tags)"

  validation {
    condition     = can(regex("^[a-z0-9-]+$", var.cluster_name))
    error_message = "cluster_name must contain only lowercase letters, numbers, and hyphens."
  }
}

variable "node_count" {
  type        = number
  description = "Number of nodes in the cluster (1-10)"
  default     = 3

  validation {
    condition     = var.node_count >= 1 && var.node_count <= 10
    error_message = "node_count must be between 1 and 10."
  }
}

variable "cpu_cores" {
  type        = number
  description = "vCPU cores per VM"
  default     = 4
}

variable "memory_mb" {
  type        = number
  description = "Memory in MB per VM"
  default     = 8192
}

variable "disk_size_gb" {
  type        = number
  description = "Root disk size in GB per VM"
  default     = 32
}

variable "os_image" {
  type        = string
  description = "Cloud image file ID on Proxmox (local:iso/...)"
  default     = "local:iso/debian-12-genericcloud-amd64-20240211-1654.img"
}
```

The existing `nodes` variable (list of Proxmox host objects) stays — it defines *where* VMs land. The new `node_count` controls *how many* VMs are created. VMs are distributed across Proxmox hosts round-robin. The `length(var.nodes) == 3` validation is removed; any number of Proxmox hosts is valid.

**Changes to `scripts/iac/proxmox/vms.tf`:**

```hcl
resource "proxmox_virtual_environment_vm" "hive_node" {
  count = var.node_count

  name        = "${var.cluster_name}-${count.index + 1}"
  description = "Managed by Terraform - cluster: ${var.cluster_name}"
  tags        = ["terraform", "hive", var.cluster_name]
  node_name   = var.nodes[count.index % length(var.nodes)].name

  cpu {
    cores = var.cpu_cores
    type  = "host"
  }

  memory {
    dedicated = var.memory_mb
  }

  disk {
    file_id      = var.os_image
    datastore_id = var.nodes[count.index % length(var.nodes)].datastore_id
    interface    = "virtio0"
    iothread     = true
    discard      = "on"
    size         = var.disk_size_gb
  }

  # ... initialization, network_device, agent blocks unchanged
}
```

**Changes to `scripts/iac/proxmox/cloud-config.tf`:**

Slim down to OS essentials only. Remove hive clone, `quickinstall`, `clone-deps.sh`, `dev-setup.sh`:

```yaml
#cloud-config
package_update: true
package_upgrade: true
packages:
  - qemu-guest-agent
  - net-tools
  - git
  - make
runcmd:
  - timedatectl set-timezone America/Los_Angeles
  - systemctl enable qemu-guest-agent
  - systemctl start qemu-guest-agent
  - ufw disable
  - sysctl -w net.core.rmem_max=4194304
  - sysctl -w net.core.wmem_max=4194304
  - usermod -aG kvm tf-user
  - echo "done" > /tmp/vendor-cloud-init-done
```

Hive installation moves to Phase 2 (Configure), where it can be iterated without re-provisioning VMs.

**New `scripts/iac/proxmox/outputs.tf`:**

Structured inventory output for the configure phase:

```hcl
output "inventory" {
  description = "Cluster inventory for configure phase"
  value = {
    cluster_name = var.cluster_name
    nodes = [
      for i, vm in proxmox_virtual_environment_vm.hive_node : {
        name       = "${var.cluster_name}-${i + 1}"
        index      = i + 1
        management = vm.ipv4_addresses[1][0]
        data       = vm.ipv4_addresses[2][0]
      }
    ]
    ssh_user         = "tf-user"
    ssh_key_path     = var.ssh_private_key_path
  }
}
```

**State isolation:**

Each cluster gets its own state file via `-state` flag:

```bash
tofu apply -state="clusters/${CLUSTER_NAME}/terraform.tfstate"
```

The `clusters/` directory is gitignored. Each cluster's state, inventory, and logs live in `clusters/{name}/`.

### Phase 2: Configure

A shell script that reads the Terraform outputs and sets up Hive on the provisioned VMs.

**`scripts/iac/configure-cluster.sh <cluster_name>`:**

1. Read inventory from `tofu output -json -state=clusters/{name}/terraform.tfstate`
2. Wait for cloud-init to complete on all nodes (poll `/tmp/vendor-cloud-init-done`)
3. On each node (parallel): clone/pull hive repo, run `make quickinstall`, `clone-deps.sh`, `dev-setup.sh`, `make build`
4. On node 1: `hive admin init` with management IPs from inventory
5. On remaining nodes (parallel): `hive admin join` pointing at node 1
6. On all nodes: `hive start`
7. Wait for health checks: poll `http://<ip>:4432/health` on all nodes

This replaces the manual SSH commands currently in `scripts/iac/proxmox/README.md`. The `hive admin init` and `hive admin join` commands are generated from inventory data rather than hardcoded IPs.

### Phase 3: Test

**`scripts/iac/test-cluster.sh <cluster_name>`:**

1. Read inventory to get node IPs
2. Set `HIVE_ENDPOINT=https://<node1-management-ip>:9999`
3. Run E2E tests from `tests/e2e/` against the real cluster
4. Save results to `clusters/{name}/test-results/`

This reuses the existing E2E test logic, currently Docker-only (`tests/e2e/run-e2e.sh`, `tests/e2e/run-multinode-e2e.sh`), pointed at real cluster IPs instead of localhost containers.

### Phase 4: Destroy

```bash
tofu destroy -state="clusters/${CLUSTER_NAME}/terraform.tfstate"
rm -rf "clusters/${CLUSTER_NAME}/"
```

### Lifecycle wrapper

**`scripts/iac/hive-test.sh`** — single entry point:

```bash
#!/bin/bash
# Usage: hive-test.sh <command> <cluster_name> [options]
#
# Commands:
#   up          Provision VMs (Phase 1)
#   configure   Install and start Hive (Phase 2)
#   test        Run E2E tests (Phase 3)
#   down        Destroy VMs and clean up (Phase 4)
#   full        Run all phases: up -> configure -> test -> down
#   status      Show cluster status (node IPs, health)
#
# Options (passed through to tofu apply):
#   --node-count=N      Number of nodes (default: 3)
#   --cpu-cores=N       vCPU per node (default: 4)
#   --memory-mb=N       Memory per node in MB (default: 8192)
#   --disk-size-gb=N    Disk per node in GB (default: 32)

# Examples:
#   hive-test.sh full my-test-cluster
#   hive-test.sh up stress-test --node-count=5 --cpu-cores=8
#   hive-test.sh down stress-test
```

The `full` command mirrors kubetest2's `--up --test --down` pattern: provision, configure, test, then tear down regardless of test outcome (with exit code preserved).

## Files to Modify

| File | Change |
|---|---|
| `scripts/iac/proxmox/variables.tf` | Remove `length == 3` validation, add `cluster_name`, `node_count`, `cpu_cores`, `memory_mb`, `disk_size_gb`, `os_image` |
| `scripts/iac/proxmox/vms.tf` | Use `count` instead of `for_each` on `local.nodes`, parameterize VM name/specs, round-robin placement |
| `scripts/iac/proxmox/cloud-config.tf` | Remove hive clone/quickinstall/dev-setup, keep OS essentials only |
| `scripts/iac/proxmox/main.tf` | No changes (provider config stays the same) |
| `scripts/iac/proxmox/outputs.tf` | **New** — structured inventory output (IPs, SSH config, cluster name) |
| `scripts/iac/configure-cluster.sh` | **New** — Phase 2: install hive, form cluster, start services |
| `scripts/iac/test-cluster.sh` | **New** — Phase 3: run E2E tests against real cluster |
| `scripts/iac/hive-test.sh` | **New** — lifecycle wrapper (up/configure/test/down/full) |
| `scripts/iac/proxmox/.gitignore` | Add `clusters/` directory |

## Multi-Environment Usage Examples

```bash
# Source Proxmox credentials (unchanged from today)
source scripts/iac/proxmox/.env

# Environment 1: standard 3-node dev cluster
./scripts/iac/hive-test.sh up hive-dev1

# Environment 2: 5-node stress test with larger VMs
./scripts/iac/hive-test.sh up hive-stress --node-count=5 --cpu-cores=8 --memory-mb=16384

# Configure and test environment 1
./scripts/iac/hive-test.sh configure hive-dev1
./scripts/iac/hive-test.sh test hive-dev1

# Full lifecycle (CI use case): provision, configure, test, destroy
./scripts/iac/hive-test.sh full hive-ci-$(date +%s)

# Check status of any cluster
./scripts/iac/hive-test.sh status hive-dev1

# Tear down when done
./scripts/iac/hive-test.sh down hive-stress
./scripts/iac/hive-test.sh down hive-dev1
```

## Testing

- Provision a 1-node cluster, verify VM comes up with correct name and specs
- Provision a 3-node cluster (matches current setup), verify parity with existing behavior
- Run configure phase, verify all nodes have hive running and cluster formed
- Run test phase, verify E2E tests execute against real cluster
- Run destroy phase, verify VMs are removed and state is cleaned
- Provision two clusters simultaneously, verify no naming or state conflicts
- Test the `full` lifecycle end-to-end

## Future Work

- **CI integration**: GitHub Actions workflow that provisions real Proxmox VMs for E2E tests on PR, with automatic teardown
- **AWS provider parity**: Same lifecycle wrapper for a future `scripts/iac/aws/` backend, keeping the `up/configure/test/down` contract
- **Test matrices**: Parameterized runs varying node count and instance sizes across a matrix (like avocado), e.g. `{1,3,5} nodes x {4,8} cores`
- **Ansible migration**: Replace `configure-cluster.sh` with Ansible playbooks when shell scripting complexity grows (inventory.json maps cleanly to Ansible inventory)
- **Re-configure without re-provision**: Since cloud-init no longer installs hive, `configure` can be re-run to deploy a new hive build without destroying/recreating VMs
