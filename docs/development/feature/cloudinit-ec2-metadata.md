# Cloud-Init and EC2 Metadata Service Architecture

This document analyzes Hive's current cloud-init implementation, compares it with industry standards (AWS, OpenStack), and proposes a migration path to fully leverage the EC2 metadata service.

## Current Implementation

### Overview

Hive currently uses a **hybrid approach**:

1. **NoCloud datasource** (cidata ISO) — provides initial bootstrap data
2. **EC2 metadata service** — provides runtime instance data at `10.0.2.100` (SLIRP) or `169.254.169.254` (TAP)

### NoCloud ISO Contents

The cidata ISO contains two files:

**user-data** (`#cloud-config` format):
```yaml
#cloud-config
users:
  - name: ec2-user
    shell: /bin/bash
    groups: [sudo]
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    ssh_authorized_keys:
      - <SSH_PUBLIC_KEY>

hostname: hive-vm-<instance-id>
manage_etc_hosts: true

# Point cloud-init to our metadata service (SLIRP workaround)
datasource:
  Ec2:
    metadata_urls:
      - http://10.0.2.100

# Optional: custom user-data (cloud-config or script)
```

**meta-data** (YAML):
```yaml
instance-id: i-025a0cf12b0d0a633
local-hostname: hive-vm-025a0cf12b0d0a63
```

### EC2 Metadata Service

The metadata server (`metadata.go`) provides IMDSv2-compatible endpoints:

| Endpoint | Description |
|----------|-------------|
| `PUT /latest/api/token` | Generate session token (IMDSv2) |
| `GET /latest/meta-data/` | Instance metadata tree |
| `GET /latest/user-data` | User-provided data |
| `GET /latest/dynamic/instance-identity/document` | Instance identity JSON |

**Implemented metadata paths:**
- `ami-id`, `instance-id`, `instance-type`
- `hostname`, `local-hostname`, `local-ipv4`, `public-ipv4`
- `placement/availability-zone`, `placement/region`
- `mac`, `network/interfaces/macs/<mac>/...`
- `block-device-mapping/`, `security-groups`
- `reservation-id`, `services/domain`, `services/partition`

### Code Locations

| Component | File | Purpose |
|-----------|------|---------|
| ISO generation | `hive/handlers/ec2/instance/service_impl.go` | Creates cidata ISO |
| Metadata server | `hive/daemon/metadata.go` | HTTP server for IMDS |
| Templates | `service_impl.go:29-72` | cloud-config and meta-data templates |

---

## Industry Standards

### AWS EC2

AWS uses **only** the metadata service — no config drive:

1. **Instance launches** with network configured via DHCP
2. **cloud-init** queries `169.254.169.254` for all data
3. **user-data** retrieved from `/latest/user-data`
4. **SSH keys** retrieved from `/latest/meta-data/public-keys/`
5. **IMDSv2** required by default (session tokens)

**Key insight**: AWS instances don't need a config drive because networking is pre-configured and the metadata service is always reachable.

Sources:
- [EC2 Instance Metadata](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html)
- [IMDSv2 by Default](https://aws.amazon.com/blogs/aws/amazon-ec2-instance-metadata-service-imdsv2-by-default/)

### OpenStack Nova

OpenStack supports **both** methods:

**1. Config Drive** (label: `config-2`)
- Attached as ISO/vfat filesystem
- Contains `openstack/latest/meta_data.json`, `user_data`, `network_data.json`
- Works without network connectivity
- Max size: 64MB

**2. Metadata Service** (`169.254.169.254`)
- Requires neutron metadata agent + network connectivity
- Dynamic data (can change during instance lifetime)
- Same data as config drive

**When to use each:**
- Config drive: No DHCP, network bootstrapping, high metadata load
- Metadata service: Dynamic updates, smaller attack surface

Sources:
- [Nova Config Drives](https://docs.openstack.org/nova/latest/admin/config-drive.html)
- [Nova Metadata](https://docs.openstack.org/nova/latest/user/metadata.html)
- [cloud-init OpenStack Datasource](https://cloudinit.readthedocs.io/en/latest/reference/datasources/openstack.html)

### Cloud-Init Datasource Priority

Cloud-init checks datasources in priority order:

1. **NoCloud** — local filesystem (cidata/config-2 volumes)
2. **ConfigDrive** — OpenStack config drive
3. **EC2** — AWS-style metadata service
4. **OpenStack** — OpenStack metadata service
5. Others (GCE, Azure, etc.)

The first datasource that "claims" the instance wins.

Sources:
- [cloud-init Datasources](https://cloudinit.readthedocs.io/en/latest/reference/datasources.html)
- [NoCloud Datasource](https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html)

---

## Gap Analysis

### What We Have vs. What's Needed

| Feature | Current (NoCloud ISO) | EC2 Metadata | Gap |
|---------|----------------------|--------------|-----|
| SSH keys | ✅ In user-data | ✅ `/meta-data/public-keys/` | Need to serve from metadata |
| Hostname | ✅ In meta-data | ✅ `/meta-data/hostname` | ✅ Already served |
| User data | ✅ In user-data file | ✅ `/user-data` | ✅ Already served |
| Instance ID | ✅ In meta-data | ✅ `/meta-data/instance-id` | ✅ Already served |
| Network config | ❌ Not provided | ❌ Not implemented | Need network-config |
| Instance identity | ❌ N/A | ✅ `/dynamic/instance-identity/` | ✅ Already served |
| IAM credentials | ❌ N/A | ❌ Not implemented | Future: `/meta-data/iam/` |

### SSH Keys via Metadata

AWS serves SSH keys at:
```
/latest/meta-data/public-keys/
/latest/meta-data/public-keys/0/openssh-key
```

**Current gap**: We embed the SSH key in the cidata user-data. Cloud-init can also fetch it from the metadata service if we implement the `public-keys/` endpoint.

### Network Configuration

AWS DHCP provides:
- IP address, netmask, gateway
- DNS servers
- NTP servers
- Hostname

For TAP networking, we'll need either:
- DHCP server on the bridge (like libvirt's `dnsmasq`)
- Static network config in cloud-init (`network-config` file)

---

## Proposed Architecture Changes

### Option A: Metadata-Only (AWS Style)

Remove the cidata ISO entirely; serve everything via metadata service.

**Requirements:**
1. TAP networking with DHCP (or static config in AMI)
2. `169.254.169.254` reachable before cloud-init runs
3. SSH keys served at `/meta-data/public-keys/`
4. User-data served at `/user-data`

**Pros:**
- Simpler — no ISO generation
- Dynamic — can update metadata without reboot
- AWS-compatible — standard cloud-init EC2 datasource
- Smaller boot footprint — no extra volume

**Cons:**
- Requires working network before cloud-init
- Can't bootstrap network config (chicken-and-egg)
- Metadata service must be highly available

### Option B: Config Drive + Metadata (OpenStack Style)

Keep cidata ISO for bootstrap, use metadata for runtime.

**Current approach** — already implemented.

**Enhancements needed:**
1. Add SSH keys to metadata service (redundancy)
2. Add `network-config` to cidata for TAP networking
3. Optionally add `vendor-data` for Hive-specific config

**Pros:**
- Works without network (bootstrap)
- Fallback if metadata service unavailable
- Can configure network before DHCP

**Cons:**
- Two data sources to maintain
- ISO must be regenerated for changes
- Slightly more complex

### Option C: Minimal ISO + Full Metadata (Recommended)

Reduce cidata to minimal bootstrap; move bulk of config to metadata.

**cidata contains only:**
```yaml
# user-data
#cloud-config
datasource:
  Ec2:
    metadata_urls:
      - http://169.254.169.254  # TAP mode
      - http://10.0.2.100       # SLIRP fallback
```

```yaml
# meta-data
instance-id: i-xxx
```

Optional `network-config` for TAP mode:
```yaml
version: 2
ethernets:
  eth0:
    dhcp4: true
```

**Metadata service provides:**
- SSH public keys (`/meta-data/public-keys/`)
- Hostname, IPs, placement
- User-data (passed via RunInstances)
- Instance identity document
- All other EC2-compatible metadata

**Pros:**
- Minimal ISO (just bootstrap pointer)
- Full AWS SDK compatibility
- User-data changes don't require new ISO
- Clean separation of concerns

**Cons:**
- Requires metadata service for SSH keys
- Slightly more complex initial setup

---

## Implementation Plan

### Phase 1: Add Missing Metadata Endpoints

Add SSH public keys endpoint to `metadata.go`:

```go
// /latest/meta-data/public-keys/
case "public-keys", "public-keys/":
    // List key indices
    return "0=hive-key"

case "public-keys/0", "public-keys/0/":
    return "openssh-key"

case "public-keys/0/openssh-key":
    // Return the SSH public key from RunInstancesInput.KeyName
    return instance.SSHPublicKey  // Need to store this
```

### Phase 2: Store User-Data in Instance

Currently user-data is baked into the ISO. Change to:

1. Store raw user-data in `vm.VM.UserData` (already done)
2. Serve via `/latest/user-data` endpoint (already done)
3. Remove user-data from cidata ISO
4. Let cloud-init fetch from metadata service

### Phase 3: Simplify cidata ISO

Reduce ISO to minimal bootstrap:

```go
const cloudInitUserDataTemplate = `#cloud-config
# Minimal bootstrap - points cloud-init to metadata service
datasource:
  Ec2:
    metadata_urls:
      - http://169.254.169.254
      - http://10.0.2.100
    max_wait: 60
    timeout: 10
`

const cloudInitMetaTemplate = `instance-id: {{.InstanceID}}
`
```

### Phase 4: Network Configuration (TAP Mode)

Add `network-config` file to cidata for TAP networking:

```go
const cloudInitNetworkTemplate = `version: 2
ethernets:
  {{.Interface}}:
    dhcp4: {{.UseDHCP}}
    {{if not .UseDHCP}}
    addresses:
      - {{.IPAddress}}/{{.Netmask}}
    gateway4: {{.Gateway}}
    nameservers:
      addresses:
        - {{.DNS}}
    {{end}}
`
```

### Phase 5: Remove ISO Dependency (Optional)

For pure metadata-only mode (requires TAP + DHCP):

1. Configure libvirt/dnsmasq to provide DHCP on bridge
2. Remove cidata ISO attachment
3. Rely entirely on EC2 datasource

---

## Data Flow Comparison

### Current Flow (Hybrid)

```
1. VM boots
2. cloud-init finds cidata ISO (NoCloud datasource)
3. Reads user-data: SSH keys, hostname, users
4. Reads meta-data: instance-id
5. Applies configuration
6. (Optional) Queries metadata service for additional data
```

### Proposed Flow (Metadata-Primary)

```
1. VM boots
2. cloud-init finds cidata ISO (minimal)
3. Reads datasource config: metadata_urls
4. Queries 169.254.169.254 (or 10.0.2.100)
5. Fetches: SSH keys, user-data, hostname, etc.
6. Applies configuration
```

---

## Best Practices Summary

### From AWS

1. **IMDSv2 mandatory** — use session tokens (already implemented)
2. **Hop limit** — set to 2 for container environments
3. **Don't store secrets** — metadata is not encrypted
4. **Instance identity documents** — signed JSON for verification

### From OpenStack

1. **Config drive for bootstrap** — network config before DHCP
2. **Metadata for runtime** — dynamic updates
3. **Label consistency** — `config-2` for OpenStack, `cidata` for NoCloud
4. **genisoimage dependency** — required for ISO generation

### For Hive

1. **Support both modes** — SLIRP (dev) and TAP (prod) networking
2. **Minimal ISO** — just enough to find metadata service
3. **Full metadata implementation** — SSH keys, user-data, identity
4. **IMDSv2 only** — security best practice

---

## Configuration Reference

### Proposed Config Structure

```yaml
# config.yaml
cloud_init:
  # "iso" = generate cidata ISO (current)
  # "metadata" = metadata service only (requires TAP+DHCP)
  # "hybrid" = minimal ISO + full metadata (recommended)
  mode: "hybrid"

  # For hybrid/iso modes
  iso:
    include_ssh_keys: false      # Fetch from metadata instead
    include_user_data: false     # Fetch from metadata instead
    include_network_config: true # Required for TAP without DHCP
```

---

## References

### Cloud-Init Documentation
- [Datasources Overview](https://cloudinit.readthedocs.io/en/latest/reference/datasources.html)
- [NoCloud Datasource](https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html)
- [EC2 Datasource](https://cloudinit.readthedocs.io/en/latest/reference/datasources/ec2.html)
- [OpenStack Datasource](https://cloudinit.readthedocs.io/en/latest/reference/datasources/openstack.html)
- [Network Config v2](https://cloudinit.readthedocs.io/en/latest/reference/network-config-format-v2.html)

### AWS Documentation
- [EC2 Instance Metadata](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html)
- [IMDS Configuration](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configuring-instance-metadata-service.html)
- [IMDSv2 Migration](https://aws.amazon.com/blogs/aws/amazon-ec2-instance-metadata-service-imdsv2-by-default/)

### OpenStack Documentation
- [Nova Config Drives](https://docs.openstack.org/nova/latest/admin/config-drive.html)
- [Nova Metadata](https://docs.openstack.org/nova/latest/user/metadata.html)
- [Ironic Config Drive](https://docs.openstack.org/ironic/latest/install/configdrive.html)
