# Proxmox IaC

OpenTofu/Terraform templates for provisioning a 3-node Hive development cluster on Proxmox VE.

## Prerequisites

- [OpenTofu](https://opentofu.org/) (or Terraform >= 1.6.0)
- 3 Proxmox VE nodes with:
  - A Terraform API token (`Datacenter > Permissions > API Tokens`)
  - SSH access for a `terraform` user on each node
  - Debian 12 cloud image uploaded to `local:iso/` on each node
- SSH keypair for cloud-init VM access
- A hardcoded Debian ISO is used, which needs to be pre-installed on each Proxmox host ("debian-12-genericcloud-amd64-20240211-1654.img") https://cdimage.debian.org/images/cloud/bookworm/20240211-1654/debian-12-genericcloud-amd64-20240211-1654.qcow2

## Setup

1. Copy the example environment file and fill in your values:

```sh
cp .env.example .env
# Edit .env with your Proxmox endpoint, SSH keys, and node configuration
```

2. Set your Proxmox API token separately (do not store in `.env`):

```sh
export PROXMOX_VE_API_TOKEN="terraform@pve!provider=YOUR_TOKEN_SECRET"
```

3. Initialize and deploy:

```sh
source .env
tofu init
tofu plan
tofu apply
```

### Example `.env` file

```sh
# --- Proxmox API ---

# API token - set manually via: export PROXMOX_VE_API_TOKEN="terraform@pve!provider=..."
# Format: <user>@<realm>!<token-name>=<token-secret>

# Proxmox VE API endpoint (the URL you use to access the web UI)
export TF_VAR_proxmox_endpoint="https://pve1.lab.example.com:8006/"

# --- SSH Access ---

# SSH username on the Proxmox hosts (used by the provider for file uploads via SCP)
export TF_VAR_proxmox_ssh_username="terraform"

# Path to SSH private key that authenticates as the above user on each Proxmox host
export TF_VAR_ssh_private_key_path="~/.ssh/proxmox-tf"

# Path to SSH public key injected into VMs via cloud-init (the tf-user account)
export TF_VAR_ssh_public_key_path="~/.ssh/proxmox-tf-cloudinit.pub"

# --- Cluster Nodes (exactly 3 required) ---
#
# Each node object:
#   name         - Proxmox node name exactly as shown in the Proxmox UI sidebar
#   address      - SSH-reachable hostname or IP of the Proxmox host
#   bridge       - Linux bridge for VM network interfaces (check Proxmox UI > Node > Network)
#   datastore_id - Proxmox storage pool for VM disks (check Proxmox UI > Node > Storage)

export TF_VAR_nodes='[
  {
    "name": "pve1",
    "address": "pve1.lab.example.com",
    "bridge": "vmbr0",
    "datastore_id": "local-lvm"
  },
  {
    "name": "pve2",
    "address": "pve2.lab.example.com",
    "bridge": "vmbr0",
    "datastore_id": "local-lvm"
  },
  {
    "name": "pve3",
    "address": "pve3.lab.example.com",
    "bridge": "vmbr0",
    "datastore_id": "local-lvm"
  }
]'
```

### Environment variable reference

| Variable | Required | Description |
|---|---|---|
| `PROXMOX_VE_API_TOKEN` | Yes | Proxmox API token (read by provider directly) |
| `TF_VAR_proxmox_endpoint` | Yes | Proxmox VE API URL (e.g. `https://host:8006/`) |
| `TF_VAR_proxmox_ssh_username` | No | SSH user on Proxmox hosts (default: `terraform`) |
| `TF_VAR_ssh_private_key_path` | Yes | SSH private key for Proxmox host access |
| `TF_VAR_ssh_public_key_path` | Yes | SSH public key injected into VMs via cloud-init |
| `TF_VAR_nodes` | Yes | JSON array of exactly 3 node objects |

Each node object in `TF_VAR_nodes`:

| Field | Example | Description |
|---|---|---|
| `name` | `pve1` | Proxmox node name (as shown in UI sidebar) |
| `address` | `pve1.lab.example.com` | SSH-reachable hostname or IP |
| `bridge` | `vmbr0` | Network bridge for VM interfaces |
| `datastore_id` | `local-lvm` | Storage pool for VM disks |

## Deploy

```sh
source .env
tofu init
tofu plan    # validates config - fails if required env vars are missing
tofu apply
```

Example output:

```sh
Plan: 6 to add, 0 to change, 0 to destroy.

Apply complete! Resources: 6 added, 0 changed, 0 destroyed.

Outputs:

iac_mulgaos_ips = {
  "node1" = {
    "data" = "10.1.3.169"
    "management" = "10.1.3.170"
  }
  "node2" = {
    "data" = "10.1.2.21"
    "management" = "10.1.3.171"
  }
  "node3" = {
    "data" = "10.1.2.22"
    "management" = "10.1.3.172"
  }
}
```

## Destroy

```sh
source .env
tofu destroy
```

## SSH access

Connect to provisioned VMs using the cloud-init public key:

```sh
ssh -i ~/.ssh/your-cloud-init-key tf-user@<VM_IP>
```

## Multi-node cluster setup

After VMs are provisioned, initialize the Hive cluster manually. Consider moving to Ansible for automation.

Note, modify the NODE IP addresses with the output from the terraform deployment above.

### Node 1 (init)

Management traffic over first network adapter, with Open vSwitch (Geneve) traffic on the 2nd for VPC traffic.

```sh
cd ~/Development/mulga/hive
make

export HIVE_REGION="ap-southeast-2"
export HIVE_AZ="ap-southeast-2a"
export NODE1_MGMT_IP="10.1.3.170"
export NODE2_MGMT_IP="10.1.3.171"
export NODE3_MGMT_IP="10.1.3.172"

./bin/hive admin init \
  --node node1 \
  --bind $NODE1_MGMT_IP \
  --cluster-bind $NODE1_MGMT_IP \
  --cluster-routes $NODE1_MGMT_IP:4248 \
  --predastore-nodes $NODE1_MGMT_IP,$NODE2_MGMT_IP,$NODE3_MGMT_IP \
  --port 4432 \
  --hive-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region $HIVE_REGION \
  --az $HIVE_AZ
```

### Node 2 (join)

```sh
cd ~/Development/mulga/hive
make

export HIVE_REGION="ap-southeast-2"
export HIVE_AZ="ap-southeast-2"
export NODE1_MGMT_IP="10.1.3.170"
export NODE2_MGMT_IP="10.1.3.171"
export NODE3_MGMT_IP="10.1.3.172"

./bin/hive admin join \
  --node node2 \
  --bind $NODE2_MGMT_IP \
  --cluster-bind $NODE2_MGMT_IP \
  --cluster-routes $NODE1_MGMT_IP:4248 \
  --host $NODE1_MGMT_IP:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region $HIVE_REGION \
  --az $HIVE_AZ
```

### Node 3 (join)

```sh
cd ~/Development/mulga/hive
make

export HIVE_REGION="ap-southeast-2"
export HIVE_AZ="ap-southeast-2"
export NODE1_MGMT_IP="10.1.3.170"
export NODE2_MGMT_IP="10.1.3.171"
export NODE3_MGMT_IP="10.1.3.172"

./bin/hive admin join \
  --node node3 \
  --bind $NODE3_MGMT_IP \
  --cluster-bind $NODE3_MGMT_IP \
  --cluster-routes $NODE1_MGMT_IP:4248 \
  --host $NODE1_MGMT_IP:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region $HIVE_REGION \
  --az $HIVE_AZ
```

## Known issues

- `~/hive/config/hive.toml` - Does not add node1, node2, node3 from config automatically
- `~/hive/config/predastore/predastore.toml` - Uses previous static local node config, needs to use the IPs for each node in the cluster
- On multi-node deployments, NATS on the primary can timeout waiting for other nodes to start, causing a race condition where NATS fails and all dependent services fail
- When adding 3 nodes, `nats.conf` is not updated with the 3rd node. Each node's cluster name is hardcoded to `C1` instead of per-node names
