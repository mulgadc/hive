# Hive Infrastructure Setup Guide

> **Purpose**: Step-by-step instructions for setting up Open vSwitch (VPC) and JetStream (IAM) on Hive nodes.
> This guide can be run by an AI agent or manually when setting up new machines.

## Prerequisites

- Debian 12 (Bookworm) or Ubuntu 22.04+
- Root/sudo access
- NATS server running with JetStream enabled

## 1. Open vSwitch Setup for VPC

### 1.1 Install Open vSwitch

```bash
# Update package list
sudo apt update

# Install Open vSwitch
sudo apt install -y openvswitch-switch openvswitch-common

# Verify installation
sudo ovs-vsctl --version
```

### 1.2 Create Main VPC Bridge

```bash
# Create the main Hive VPC bridge (if not exists)
sudo ovs-vsctl --may-exist add-br hive-vpc0

# Set the bridge to use normal MAC learning
sudo ovs-vsctl set Bridge hive-vpc0 stp_enable=false

# Enable NetFlow for monitoring (optional)
# sudo ovs-vsctl -- set Bridge hive-vpc0 netflow=@nf -- --id=@nf create NetFlow targets=\"127.0.0.1:2055\"

# Verify bridge creation
sudo ovs-vsctl show
```

### 1.3 Configure VLAN Support for Subnet Isolation

```bash
# VLANs are used to isolate different VPCs/subnets
# VLAN IDs will be assigned dynamically by the Hive VPC service:
# - VLAN 1: Default VPC (10.0.0.0/16)
# - VLAN 2-4094: User-created VPCs

# Set bridge to handle 802.1Q VLAN tags
sudo ovs-vsctl set Bridge hive-vpc0 vlan_mode=trunk

# Verify VLAN configuration
sudo ovs-vsctl list Bridge hive-vpc0
```

### 1.4 Create Internal Ports for Host Communication

```bash
# Create internal port for host access to VPC network
sudo ovs-vsctl --may-exist add-port hive-vpc0 hive-int0 -- set Interface hive-int0 type=internal

# Assign IP to internal port (gateway for default subnet)
sudo ip addr add 10.0.1.1/24 dev hive-int0 || true
sudo ip link set hive-int0 up

# Enable IP forwarding (required for NAT/routing)
sudo sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" | sudo tee -a /etc/sysctl.d/99-hive.conf

# Verify
ip addr show hive-int0
```

### 1.5 Configure NAT for Internet Access (Optional)

```bash
# If VMs need internet access through NAT:
# Replace eth0 with your actual external interface

EXTERNAL_IF=$(ip route | grep default | awk '{print $5}')
echo "External interface: $EXTERNAL_IF"

# Add NAT rule
sudo iptables -t nat -A POSTROUTING -s 10.0.0.0/8 -o $EXTERNAL_IF -j MASQUERADE

# Persist iptables rules
sudo apt install -y iptables-persistent
sudo netfilter-persistent save
```

### 1.6 OpenFlow Rules for Security Groups

```bash
# Security groups are implemented via OpenFlow rules
# The Hive VPC service will manage these dynamically

# Example: Drop all traffic by default (fail-closed)
sudo ovs-ofctl add-flow hive-vpc0 "priority=0,actions=drop"

# Example: Allow established connections
sudo ovs-ofctl add-flow hive-vpc0 "priority=100,ct_state=+est+trk,actions=normal"

# Example: Allow traffic within same VLAN (same subnet)
sudo ovs-ofctl add-flow hive-vpc0 "priority=50,dl_vlan=1,actions=normal"

# View current flows
sudo ovs-ofctl dump-flows hive-vpc0
```

### 1.7 Verify Open vSwitch Setup

```bash
# Full status check
echo "=== OVS Version ==="
sudo ovs-vsctl --version

echo "=== OVS Configuration ==="
sudo ovs-vsctl show

echo "=== Bridge Details ==="
sudo ovs-vsctl list Bridge hive-vpc0

echo "=== OpenFlow Flows ==="
sudo ovs-ofctl dump-flows hive-vpc0

echo "=== Interface Status ==="
ip addr show hive-int0
```

## 2. JetStream Setup for IAM

### 2.1 Verify NATS JetStream is Enabled

```bash
# Check NATS server config
cat ~/hive/config/nats/*.conf | grep -A10 jetstream

# Should show jetstream configuration enabled
# If not, add to nats.conf:
# jetstream {
#   store_dir: /path/to/jetstream
#   max_mem: 1G
#   max_file: 10G
# }
```

### 2.2 Create IAM KV Buckets

```bash
# Use NATS CLI to create KV buckets for IAM
# These will store users, roles, and policies

# Users bucket
nats kv add hive-iam-users --replicas 1 --history 10 --ttl 0

# Roles bucket
nats kv add hive-iam-roles --replicas 1 --history 10 --ttl 0

# Policies bucket
nats kv add hive-iam-policies --replicas 1 --history 10 --ttl 0

# Access keys bucket (maps access key ID to user)
nats kv add hive-iam-access-keys --replicas 1 --history 10 --ttl 0

# Sessions bucket (for temporary credentials)
nats kv add hive-iam-sessions --replicas 1 --history 5 --ttl 3600

# Verify buckets
nats kv ls
```

### 2.3 Create Default Admin User

```bash
# Create default admin user (stored in JetStream KV)
# Format: JSON with user details

# Generate access key pair
ACCESS_KEY_ID="AKIA$(openssl rand -hex 10 | tr 'a-z' 'A-Z')"
SECRET_ACCESS_KEY=$(openssl rand -base64 30)

echo "Admin Access Key ID: $ACCESS_KEY_ID"
echo "Admin Secret Access Key: $SECRET_ACCESS_KEY"

# Store admin user
nats kv put hive-iam-users admin "{
  \"username\": \"admin\",
  \"arn\": \"arn:aws:iam::000000000000:user/admin\",
  \"created_at\": \"$(date -Iseconds)\",
  \"groups\": [\"administrators\"],
  \"policies\": [\"AdministratorAccess\"],
  \"access_keys\": [\"$ACCESS_KEY_ID\"]
}"

# Store access key mapping
nats kv put hive-iam-access-keys "$ACCESS_KEY_ID" "{
  \"access_key_id\": \"$ACCESS_KEY_ID\",
  \"secret_access_key\": \"$SECRET_ACCESS_KEY\",
  \"username\": \"admin\",
  \"status\": \"Active\",
  \"created_at\": \"$(date -Iseconds)\"
}"

# Create AdministratorAccess policy
nats kv put hive-iam-policies AdministratorAccess "{
  \"name\": \"AdministratorAccess\",
  \"arn\": \"arn:aws:iam::000000000000:policy/AdministratorAccess\",
  \"version\": \"2012-10-17\",
  \"statement\": [{
    \"effect\": \"Allow\",
    \"action\": \"*\",
    \"resource\": \"*\"
  }]
}"
```

### 2.4 Verify IAM Setup

```bash
echo "=== IAM KV Buckets ==="
nats kv ls

echo "=== Users ==="
nats kv get hive-iam-users admin

echo "=== Policies ==="
nats kv get hive-iam-policies AdministratorAccess

echo "=== Access Keys ==="
nats kv keys hive-iam-access-keys
```

## 3. Service Configuration

### 3.1 Update Hive Config for VPC

Add to `~/hive/config/hive.toml`:

```toml
[vpc]
enabled = true
bridge_name = "hive-vpc0"
default_cidr = "10.0.0.0/16"
default_subnet_cidr = "10.0.1.0/24"
gateway_ip = "10.0.1.1"
nat_enabled = true

[iam]
enabled = true
kv_bucket_users = "hive-iam-users"
kv_bucket_roles = "hive-iam-roles"
kv_bucket_policies = "hive-iam-policies"
kv_bucket_access_keys = "hive-iam-access-keys"
kv_bucket_sessions = "hive-iam-sessions"
```

### 3.2 Restart Hive Services

```bash
# Stop services
~/hive/scripts/stop-dev.sh

# Wait for clean shutdown
sleep 5

# Start services
~/hive/scripts/start-dev.sh

# Verify services
pgrep -a hive
```

## 4. Testing

### 4.1 Test VPC Network

```bash
# From the host, ping the gateway
ping -c 3 10.0.1.1

# Check OVS bridge is active
sudo ovs-vsctl show

# Verify flows
sudo ovs-ofctl dump-flows hive-vpc0
```

### 4.2 Test IAM via AWS CLI

```bash
# Configure AWS CLI with the admin credentials created above
aws configure --profile hive-admin
# Enter the ACCESS_KEY_ID and SECRET_ACCESS_KEY from step 2.3

# Test IAM operations (once implemented)
# aws --profile hive-admin iam get-user
# aws --profile hive-admin iam list-users
```

## 5. Troubleshooting

### OVS Issues

```bash
# Check OVS logs
sudo journalctl -u openvswitch-switch

# Restart OVS
sudo systemctl restart openvswitch-switch

# Clear all flows (caution!)
sudo ovs-ofctl del-flows hive-vpc0

# Remove and recreate bridge
sudo ovs-vsctl del-br hive-vpc0
# Then re-run section 1.2
```

### JetStream Issues

```bash
# Check NATS logs
journalctl -u nats-server

# Verify JetStream status
nats account info

# List all streams
nats stream ls

# List all KV buckets
nats kv ls
```

## 6. Multi-Node Setup Notes

When adding additional nodes:

1. Each node runs its own OVS bridge (`hive-vpc0`)
2. VLANs must be consistent across nodes
3. For cross-node connectivity, use VXLAN or GRE tunnels:

```bash
# Create VXLAN tunnel to another node (example)
sudo ovs-vsctl add-port hive-vpc0 vxlan0 -- set Interface vxlan0 type=vxlan options:remote_ip=10.1.2.18

# For multi-node JetStream, configure NATS cluster mode
# See NATS documentation for cluster configuration
```

---

**Document Version**: 1.0
**Last Updated**: 2026-01-30
**Author**: AI Agent (Claude Opus 4.5)
