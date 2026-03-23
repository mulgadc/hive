---
title: "VPC Networking"
description: "How Spinifex implements AWS-compatible VPC networking with public and private subnets, security groups, and Elastic IPs using OVN."
category: "Environments"
tags:
  - vpc
  - networking
  - ovn
  - public-subnet
  - security-groups
---

# VPC Networking

Spinifex provides AWS-compatible VPC networking on bare-metal. Every EC2 instance
runs inside an isolated virtual network backed by OVN (Open Virtual Network).
Instances can operate in two modes: **private** (overlay-only, no WAN access) or
**public** (routable from the WAN with a unique public IP).

# How It Works

Spinifex maps AWS VPC concepts directly to OVN constructs:

| AWS Concept | OVN Construct | What It Does |
|-------------|---------------|--------------|
| VPC | Logical Router | Isolates tenant networks, routes between subnets |
| Subnet | Logical Switch + DHCP | L2 broadcast domain with automatic IP assignment |
| ENI | Logical Switch Port | Per-instance network interface with MAC/IP binding |
| Internet Gateway | External Switch + NAT | Connects VPC router to physical WAN |
| Security Group | Port Group + ACLs | Stateful firewall rules enforced in OVS datapath |
| Elastic IP | `dnat_and_snat` NAT rule | Static 1:1 NAT between public and private IP |

## Network Path

```
                        Physical Network (WAN)
                               │
                        ┌──────┴──────┐
                        │ br-external │  OVS bridge (physical uplink)
                        └──────┬──────┘
                               │  ovn-bridge-mappings: external:br-external
                               │
                    ┌──────────┴──────────────────────────┐
                    │     VPC Logical Router               │
                    │                                      │
                    │  Gateway port ──── ext switch ─────── localnet port
                    │    (public IP)     (OVN)              (maps to br-external)
                    │                                      │
                    │  NAT rules:                          │
                    │    SNAT: VPC CIDR → gateway IP        │
                    │    dnat_and_snat: public ↔ private    │
                    │                                      │
                    │  Routes:                             │
                    │    0.0.0.0/0 → WAN gateway            │
                    │                                      │
                    ├────────────┬──────────────┤          │
                    │            │              │          │
              ┌─────┴────┐ ┌────┴─────┐        │          │
              │ subnet-a │ │ subnet-b │        │          │
              │ (public) │ │ (private)│        │          │
              │10.0.1/24 │ │10.0.2/24 │        │          │
              │  eni-1   │ │  eni-3   │        │          │
              │  eni-2   │ │  eni-4   │        │          │
              └──────────┘ └──────────┘        │          │
                                               └──────────┘
```

Cross-host traffic uses **Geneve tunnels** (UDP 6081) over the management/overlay
NIC. Each host runs `ovn-controller` which programs OpenFlow rules on `br-int`
(the integration bridge where all VM TAP devices connect).

# Private vs Public Subnets

A subnet's behavior depends on two things: whether the VPC has an Internet
Gateway, and whether the subnet has `MapPublicIpOnLaunch` enabled.

## Private Subnet (default)

Instances get a private IP only. They can communicate with other instances in the
same VPC (even across subnets and hosts via the overlay). They cannot reach the
internet or be reached from the WAN.

```
Instance (10.0.2.5) ──→ VPC Router ──→ [no default route] ──→ dropped
```

If the VPC has an IGW attached, private subnet instances CAN reach the internet
via the VPC router's SNAT rule (outbound only — they share the gateway IP). They
still cannot be reached from the WAN because they have no public IP.

## Public Subnet

Instances get both a private IP and a public IP. The public IP is a 1:1 NAT
managed by OVN — the instance OS only sees its private IP.

```
Outbound: Instance (10.0.1.5) ──→ SNAT ──→ exits as 203.0.113.10
Inbound:  203.0.113.10 ──→ DNAT ──→ arrives at 10.0.1.5
```

**Requirements for a public subnet:**

1. VPC has an Internet Gateway attached
2. Subnet has `MapPublicIpOnLaunch = true`
3. External IP pool configured in `spinifex.toml`

## Comparison

| | Private Subnet | Public Subnet |
|---|---|---|
| Private IP | Yes | Yes |
| Public IP | No | Auto-assigned from pool |
| Outbound internet | Only if VPC has IGW (shared SNAT) | Yes (own public IP via SNAT) |
| Inbound from WAN | No | Yes (via 1:1 NAT to public IP) |
| Instance sees public IP? | N/A | No — only sees private IP |
| Elastic IP support | Only if explicitly associated | Yes |

# External Connectivity Modes

The `[network]` section in `spinifex.toml` controls how VMs reach the outside
world. There are three modes.

## `pool` — Full Public Networking (Recommended)

The admin defines a range of routable IPs that Spinifex manages exclusively.
Supports the full AWS feature set: public subnets, auto-assign public IPs,
Elastic IPs, and bidirectional 1:1 NAT.

**Use when:** You have a block of IPs you control — datacenter ISP allocation,
homelab range carved out of your router's DHCP scope, enterprise DMZ range.

**Requirement:** The IP range must NOT be served by any other DHCP server. In a
homelab, shrink your router's DHCP scope to exclude the Spinifex range.

```toml
[network]
external_mode = "pool"

[[network.external_pools]]
name        = "wan"
range_start = "192.168.1.150"
range_end   = "192.168.1.250"
gateway     = "192.168.1.1"
prefix_len  = 24
```

## `nat` — Outbound Only (Simple)

All VMs share a single external IP for outbound SNAT. No public IPs, no Elastic
IPs, no inbound from WAN. All subnets behave as private subnets with internet
access.

The `gateway_ip` is the IP that OVN uses for SNAT. In a homelab or environment
with a router DHCP server (e.g., 192.168.1.1), `setup-ovn.sh --dhcp` obtains an
IP from the router's DHCP and uses it as the gateway IP. This is the router's
DHCP — not Spinifex's internal OVN DHCP for VMs.

**Use when:** VMs only need outbound access (apt update, pulling images). Edge
deployments behind ISP NAT. Single WAN IP available.

```toml
[network]
external_mode = "nat"

[[network.external_pools]]
name       = "wan"
gateway    = "192.168.1.1"
gateway_ip = "192.168.1.100"     # IP obtained from router DHCP or static
prefix_len = 24
```

## Disabled (empty/omitted)

VPC networking is overlay-only. No external connectivity. Instances can only
communicate within their VPC.

## Mode Comparison

| Capability | `pool` | `nat` | Disabled |
|------------|--------|-------|----------|
| Outbound internet | Yes | Yes | No |
| Inbound from WAN | Yes (1:1 NAT) | No | No |
| Public subnets | Yes | No | No |
| Auto-assign public IPs | Yes | No | No |
| Elastic IPs | Yes | No | No |
| Admin effort | Reserve IP range | Set gateway IP | None |

If you start with `nat` and later need public subnets, switch to `pool` and
define a range — no data migration needed.

# Bridge Setup — Physical Network Wiring

OVN needs an OVS bridge (`br-external`) with a physical uplink to the WAN for
external connectivity. How that uplink is provided depends on whether the host
has one NIC or multiple NICs.

## Two Bridges, Two Jobs

Every Spinifex node has two OVS bridges:

| Bridge | Purpose | Ports |
|--------|---------|-------|
| `br-int` | VM overlay traffic (Geneve tunnels) | VM TAP devices, tunnel ports |
| `br-external` | WAN uplink for public subnet traffic | macvlan on WAN NIC |

`br-int` is always created by `setup-ovn.sh`. `br-external` is created only when
`--external-bridge` is passed (required for public subnets / external connectivity).

The connection between them is logical, not physical — OVN's `localnet` port type
maps the logical external switch to `br-external` via `ovn-bridge-mappings`.

```
VM TAP ──→ br-int ──→ OVN logical pipeline ──→ localnet ──→ br-external ──→ WAN
```

## Bridge Setup (macvlan)

A macvlan sub-interface is created in bridge mode off the WAN NIC, and that
macvlan is added to br-external. The host keeps its IP on the original NIC —
SSH stays up, no risk of losing connectivity. This works for all deployments:
single-NIC homelabs, multi-NIC datacenters, and everything in between.

```
WAN NIC (host IP — unchanged)
  │
  ├── host traffic (SSH, management, overlay)
  │
  └── spx-ext-{nic} (macvlan, bridge mode, no IP)
        │
   br-external
        │
   localnet → OVN ext switch
```

### Setup

```bash
sudo setup-ovn.sh --external-bridge --external-iface=eth0
```

In environments where the WAN IP comes from a router's DHCP server (homelab,
small office), add `--dhcp` to obtain a gateway IP from the router automatically:

```bash
sudo setup-ovn.sh --external-bridge --external-iface=eth0 --dhcp
```

This requests an IP from the **router's DHCP** (e.g., 192.168.1.1 serving
addresses on the LAN). This is not Spinifex's internal OVN DHCP that assigns
private IPs to VMs — it's your network's existing DHCP server.

### What Happens

1. Creates macvlan: `ip link add spx-ext-eth0 link eth0 type macvlan mode bridge`
2. Brings it up: `ip link set spx-ext-eth0 up`
3. Creates OVS bridge `br-external`
4. Adds the macvlan (not the parent NIC) to br-external
5. Sets `ovn-bridge-mappings=external:br-external`
6. Host IP on eth0 is completely untouched
7. (If `--dhcp`) Obtains gateway IP from router DHCP for OVN SNAT

### macvlan Behavior

macvlan in `bridge` mode creates a virtual interface that shares the parent NIC's
physical wire but has its own MAC address. Frames between the macvlan and external
devices work normally. The Linux kernel blocks direct L2 frames between a parent
interface and its macvlan children.

**What this means in practice:**

| Path | Works? | Why |
|------|--------|-----|
| VM → internet | Yes | SNAT through OVN router → macvlan → WAN NIC → WAN |
| LAN device → VM public IP | Yes | LAN → WAN NIC → macvlan → br-external → OVN |
| Host → VM public IP | No | macvlan isolation (kernel blocks parent↔child) |
| Host → VM private IP | Yes | Overlay via br-int (unrelated to br-external) |

The host-to-VM-public-IP limitation is rarely a problem — the host manages VMs
via the overlay (private IPs), not their public IPs.

## How setup-ovn.sh Decides

| Flags | Result |
|-------|--------|
| `--external-bridge --external-iface=eth0` | macvlan created, added to br-external |
| (no `--external-bridge`) | Only br-int created, no WAN connectivity |

## Per-Node Configuration

Different nodes in a cluster can have different WAN NICs:

```toml
[nodes.node1.vpcd]
external_interface = "eth1"

[nodes.node2.vpcd]
external_interface = "eth0"

[nodes.node3.vpcd]
external_interface = "bond0"       # bonded NIC
```

Each node runs `setup-ovn.sh` with its own `--external-iface`. OVN doesn't
care which NIC the macvlan is on — only that `ovn-bridge-mappings` is set.

## Bridge Verification

```bash
# Check br-external exists
sudo ovs-vsctl br-exists br-external && echo "OK" || echo "MISSING"

# Check the physical port
sudo ovs-vsctl list-ports br-external
# Multi-NIC: shows "eth1" (or your WAN NIC)
# Single-NIC: shows "spx-ext-eth0" (macvlan name)

# Check bridge mappings
sudo ovs-vsctl get Open_vSwitch . external-ids:ovn-bridge-mappings
# Output: "external:br-external"

# Check macvlan (single-NIC only)
ip link show spx-ext-eth0
# Shows: state UP, type macvlan mode bridge
```

## Network Flow Diagram

```
┌──────────────────────────────────────────────────┐
│ Bare-Metal Host                                  │
│                                                  │
│  ┌──────────┐         ┌──────────┐               │
│  │ br-int   │         │br-external│              │
│  │ (overlay)│         │  (WAN)   │               │
│  │          │         │          │               │
│  │ tap-eni1 │  OVN    │spx-ext-  │               │
│  │ tap-eni2 ├──NAT───→│{nic}     │               │
│  │ tap-eni3 │pipeline │(macvlan) │               │
│  └──────────┘         └────┬─────┘               │
│                            │ macvlan bridge mode  │
│                        WAN NIC (host IP unchanged)│
└───────────────────────┼──────────────────────────┘
                        │
                   Physical WAN
```

# Configuration Reference

All network configuration lives in `spinifex.toml`. Settings are split into three
levels: cluster-wide mode, IP pool definitions, and per-node NIC settings.

## Configuration Levels

```
spinifex.toml
├── [network]                        # Cluster-wide: which mode?
│   └── external_mode = "pool"
│
├── [[network.external_pools]]       # IP ranges: what IPs to use?
│   ├── name, range_start, range_end
│   ├── gateway, prefix_len
│   └── region, az (optional)
│
└── [nodes.NAME.vpcd]                # Per-node: which NIC?
    └── external_interface
```

## Cluster-Wide: external_mode

```toml
[network]
external_mode = "pool"    # "pool", "nat", or "" (disabled)
```

| Value | Behavior |
|-------|----------|
| `"pool"` | Full public networking — public subnets, auto-assign, Elastic IPs |
| `"nat"` | Outbound-only SNAT — all VMs share one external IP |
| `""` / omitted | Overlay-only — no external connectivity |

## IP Pools: network.external_pools

Each pool defines a range of IPs that Spinifex allocates from. You can have
one pool (homelab) or many (multi-region datacenter).

```toml
[[network.external_pools]]
name        = "wan"                  # Pool identifier (unique within cluster)
range_start = "192.168.1.150"        # First allocatable IP
range_end   = "192.168.1.250"        # Last allocatable IP
gateway     = "192.168.1.1"          # WAN default gateway (next hop for 0.0.0.0/0)
gateway_ip  = ""                     # OVN router SNAT address (defaults to range_start)
prefix_len  = 24                     # Subnet mask length
region      = ""                     # Scope to region (optional)
az          = ""                     # Scope to AZ (optional)
```

### Field Details

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique pool name. Used as NATS KV key and in `AllocateAddress`. |
| `range_start` | Pool mode | First IP in the range. First IP is reserved for OVN gateway SNAT (unless `gateway_ip` overrides). |
| `range_end` | Pool mode | Last IP in the range. |
| `gateway` | Yes | Physical router/switch — the WAN default gateway. OVN sets `0.0.0.0/0 → gateway`. |
| `gateway_ip` | NAT mode | Static IP for OVN router SNAT. In pool mode, defaults to `range_start`. In NAT mode, this is the single external IP all VMs share. |
| `prefix_len` | Yes | Subnet mask for the external network (e.g., 24 = /24). |
| `region` | No | Scopes pool to a region. Instances in this region prefer this pool. |
| `az` | No | Scopes pool to an AZ. More specific than region. |

### Why range_start/range_end Instead of CIDR?

Customer IP ranges rarely align to CIDR boundaries. A datacenter might have
`203.0.113.10-203.0.113.200` from their ISP. Start/end avoids forcing admins to
calculate CIDR blocks.

### Gateway vs Gateway_IP

These are different things:

- **`gateway`** = Your network's default gateway (e.g., 192.168.1.1). This is
  where OVN sends packets destined for the internet. It's your router.
- **`gateway_ip`** = The IP that OVN uses for outbound SNAT. In pool mode,
  defaults to the first IP in the range. In NAT mode, set this explicitly.
  Must be on the same subnet as the gateway.

## Per-Node: nodes.NAME.vpcd

```toml
[nodes.spx1.vpcd]
ovn_nb_addr        = "tcp:10.1.3.181:6641"   # OVN Northbound DB
ovn_sb_addr        = "tcp:10.1.3.181:6642"   # OVN Southbound DB
external_interface = "eth1"                   # WAN NIC name
```

| Field | Description |
|-------|-------------|
| `external_interface` | Physical NIC for WAN traffic. A macvlan sub-interface is created on this NIC for br-external. Different servers may have different names (eth1, eno2, enp3s0, bond0). |

## Pool Selection Logic

When an instance needs a public IP:

1. **AZ-scoped pool first**: Pool with matching `region` + `az`
2. **Region-scoped fallback**: Pool with matching `region`, no `az` (overflow)
3. **Unscoped fallback**: Pool with no `region`/`az` (global, homelab configs)
4. **Exhausted**: All pools full → `InsufficientAddressCapacity` error

`AllocateAddress` accepts optional pool name to target a specific block
(maps to AWS `PublicIpv4Pool`).

## IPAM Storage

Each pool gets a NATS KV entry in bucket `spinifex-external-ipam`, keyed by pool
name. Allocation uses CAS (Compare-And-Set) for lock-free concurrent access:

```json
{
  "pool_name": "wan",
  "range_start": "192.168.1.150",
  "range_end": "192.168.1.250",
  "allocated": {
    "192.168.1.150": {"type": "gateway"},
    "192.168.1.151": {"type": "auto_assign", "eni_id": "eni-abc", "instance_id": "i-123"}
  }
}
```

Pools are initialized from `spinifex.toml` on vpcd startup (idempotent).

# Deployment Examples

## Homelab / Dev (Single Pool)

```
Network: 192.168.1.0/24
Router: 192.168.1.1 (DHCP .2–.149)
Spinifex: 192.168.1.150–.250 (100 IPs)
```

```toml
[network]
external_mode = "pool"

[[network.external_pools]]
name        = "wan"
range_start = "192.168.1.150"
range_end   = "192.168.1.250"
gateway     = "192.168.1.1"
prefix_len  = 24

[nodes.homelab.vpcd]
external_interface = "eth0"
```

**Setup:** Change your router's DHCP range to end at .149. Run
`setup-ovn.sh --external-bridge --external-iface=eth0`.

## Datacenter / Colo (ISP Block)

```
ISP-assigned: 203.0.113.0/28 (14 usable IPs)
ISP gateway: 203.0.113.1
Servers: 3x with separate mgmt (eth0) and public (eth1) NICs
```

```toml
[network]
external_mode = "pool"

[[network.external_pools]]
name        = "public"
range_start = "203.0.113.2"
range_end   = "203.0.113.14"
gateway     = "203.0.113.1"
prefix_len  = 28

[nodes.dc1.vpcd]
external_interface = "eth1"

[nodes.dc2.vpcd]
external_interface = "eth1"

[nodes.dc3.vpcd]
external_interface = "eno1"
```

## Enterprise On-Prem (VLAN)

```toml
[network]
external_mode = "pool"

[[network.external_pools]]
name        = "dmz"
range_start = "172.16.0.100"
range_end   = "172.16.0.200"
gateway     = "172.16.0.1"
prefix_len  = 24

[nodes.srv1.vpcd]
external_interface = "eth1.200"     # VLAN-tagged sub-interface

[nodes.srv2.vpcd]
external_interface = "bond0.200"    # Bonded NIC with VLAN tag
```

## Edge / Branch (Outbound Only)

```toml
[network]
external_mode = "nat"

[[network.external_pools]]
name       = "wan"
gateway    = "10.0.0.1"
gateway_ip = "10.0.0.50"
prefix_len = 24

[nodes.edge1.vpcd]
external_interface = "eth0"
```

## Multi-Region (Multiple Pools)

```toml
[network]
external_mode = "pool"

# US East — AZ-scoped
[[network.external_pools]]
name        = "us-east-1a"
range_start = "203.0.113.2"
range_end   = "203.0.113.254"
gateway     = "203.0.113.1"
prefix_len  = 24
region      = "us-east-1"
az          = "us-east-1a"

# US East — overflow (any AZ in region)
[[network.external_pools]]
name        = "us-east-overflow"
range_start = "192.0.2.2"
range_end   = "192.0.3.254"
gateway     = "192.0.2.1"
prefix_len  = 23
region      = "us-east-1"

# EU West
[[network.external_pools]]
name        = "eu-west"
range_start = "213.189.1.2"
range_end   = "213.189.2.254"
gateway     = "213.189.1.1"
prefix_len  = 23
region      = "eu-west-1"
```

Spinifex allocates from the correct pool based on where the instance launches.
An instance in `us-east-1a` gets an IP from `us-east-1a` first; if exhausted,
falls back to `us-east-overflow`.

# Security Groups

Security groups are stateful firewalls enforced at the OVS datapath level on
each hypervisor. Traffic is filtered before it reaches the wire — equivalent to
AWS Nitro card enforcement. The VM never sees dropped packets.

## How Security Groups Work

Each security group maps to an OVN **Port Group**. When an instance launches,
its ENI port is added to the port group(s) for its security groups. ACL rules
on the port group control traffic:

- **Default deny**: All inbound traffic dropped at priority 900
- **Allow rules**: Specific ports/protocols allowed at priority 1000 (overrides deny)
- **Stateful**: All allow rules use `allow-related` — return traffic is automatically permitted

## Default Security Group

Every VPC gets a default security group that:
- Allows all inbound from instances in the same security group
- Allows all outbound
- Denies all other inbound

## AWS Rule → OVN ACL Translation

| AWS Security Group Rule | OVN ACL Match |
|------------------------|---------------|
| Ingress TCP/22 from 0.0.0.0/0 | `outport == @sg && ip4 && tcp.dst == 22` |
| Ingress TCP/443 from 10.0.0.0/8 | `outport == @sg && ip4 && tcp.dst == 443 && ip4.src == 10.0.0.0/8` |
| Ingress ALL from sg-other | `outport == @sg && ip4 && ip4.src == $sg_other_ip4` |
| Ingress ICMP from anywhere | `outport == @sg && ip4 && icmp4` |
| Egress ALL to 0.0.0.0/0 | `inport == @sg && ip4` |
| Default deny inbound | `outport == @sg && ip4` (priority 900, action=drop) |

## Example: Allow SSH + HTTP

```bash
# Create security group
SG=$(aws ec2 create-security-group --group-name web \
  --description "Web servers" --vpc-id $VPC \
  --query GroupId --output text)

# Allow SSH from anywhere
aws ec2 authorize-security-group-ingress --group-id $SG \
  --protocol tcp --port 22 --cidr 0.0.0.0/0

# Allow HTTP from anywhere
aws ec2 authorize-security-group-ingress --group-id $SG \
  --protocol tcp --port 80 --cidr 0.0.0.0/0

# Launch instance with this SG
aws ec2 run-instances --image-id $AMI --instance-type t3.small \
  --subnet-id $SUBNET --security-group-ids $SG --key-name mykey
```

Rule changes take effect immediately — no instance restart needed.

# Elastic IPs

Elastic IPs are static public IPs that persist across instance stop/start cycles.
Unlike auto-assigned public IPs (which change on stop/start), an Elastic IP stays
with your instance.

```bash
# Allocate
EIP=$(aws ec2 allocate-address --query AllocationId --output text)

# Associate with instance
aws ec2 associate-address --allocation-id $EIP --instance-id $INSTANCE

# Stop/start instance — same Elastic IP

# Disassociate
aws ec2 disassociate-address --association-id $ASSOC_ID

# Release back to pool
aws ec2 release-address --allocation-id $EIP
```

When you associate an Elastic IP with an instance that already has an
auto-assigned public IP, the auto-assigned IP is released and replaced.

# OVN Reference

For operators debugging or verifying the OVN topology.

## IGW Attach Creates

```bash
# External logical switch with localnet port
ovn-nbctl ls-add ext-{vpcId}
ovn-nbctl lsp-add ext-{vpcId} ext-localnet-{vpcId}
ovn-nbctl lsp-set-type ext-localnet-{vpcId} localnet
ovn-nbctl lsp-set-addresses ext-localnet-{vpcId} unknown
ovn-nbctl lsp-set-options ext-localnet-{vpcId} network_name=external

# Gateway router port with real external IP
ovn-nbctl lrp-add vpc-{vpcId} gw-{vpcId} {mac} 192.168.1.150/24

# Connect external switch to router
ovn-nbctl lsp-add ext-{vpcId} ext-rtr-{vpcId}
ovn-nbctl lsp-set-type ext-rtr-{vpcId} router
ovn-nbctl lsp-set-options ext-rtr-{vpcId} router-port=gw-{vpcId}

# Gateway chassis HA
ovn-nbctl lrp-set-gateway-chassis gw-{vpcId} chassis-1 20
ovn-nbctl lrp-set-gateway-chassis gw-{vpcId} chassis-2 15

# SNAT for all VPC traffic
ovn-nbctl lr-nat-add vpc-{vpcId} snat 192.168.1.150 10.0.0.0/16

# Default route to WAN
ovn-nbctl lr-route-add vpc-{vpcId} 0.0.0.0/0 192.168.1.1
```

## Per-Instance Public IP

```bash
# 1:1 NAT (distributed — NAT happens on VM's chassis, not gateway)
ovn-nbctl lr-nat-add vpc-{vpcId} dnat_and_snat {public_ip} {private_ip} \
  port-{eniId} {eni_mac}
```

## Security Group

```bash
# Create port group
ovn-nbctl pg-add sg-{groupId}

# Add VM ports
ovn-nbctl pg-set-ports sg-{groupId} port-{eniId1} port-{eniId2}

# Allow SSH inbound (stateful)
ovn-nbctl acl-add sg-{groupId} to-lport 1000 \
  'outport == @sg_{groupId} && ip4 && tcp.dst == 22' allow-related

# Allow all egress
ovn-nbctl acl-add sg-{groupId} from-lport 1000 \
  'inport == @sg_{groupId} && ip4' allow-related

# Default deny inbound
ovn-nbctl acl-add sg-{groupId} to-lport 900 \
  'outport == @sg_{groupId} && ip4' drop
```

## Useful Debug Commands

```bash
# List all logical routers (VPCs)
sudo ovn-nbctl lr-list

# List all logical switches (subnets + external)
sudo ovn-nbctl ls-list

# Show NAT rules for a VPC
sudo ovn-nbctl lr-nat-list vpc-{vpcId}

# Show routes for a VPC
sudo ovn-nbctl lr-route-list vpc-{vpcId}

# Show chassis (nodes) in the cluster
sudo ovn-sbctl show

# Show port bindings (which VM is on which host)
sudo ovn-sbctl find Port_Binding type="" | grep -E "logical_port|chassis"

# Check ACLs on a security group
sudo ovn-nbctl acl-list sg-{groupId}

# Check port group membership
sudo ovn-nbctl pg-get-ports sg-{groupId}
```

# Quick Start

## 1. Set Up OVN Bridges

```bash
sudo setup-ovn.sh --external-bridge --external-iface=eth0
```

## 2. Configure External IP Pool

```bash
spx admin init
# Follow prompts — auto-detects NICs, suggests IP pool range
# Or edit spinifex.toml manually
```

## 3. Create VPC with Public Subnet

```bash
VPC=$(aws ec2 create-vpc --cidr-block 10.200.0.0/16 \
  --query Vpc.VpcId --output text)

SUBNET=$(aws ec2 create-subnet --vpc-id $VPC \
  --cidr-block 10.200.1.0/24 \
  --query Subnet.SubnetId --output text)

IGW=$(aws ec2 create-internet-gateway \
  --query InternetGateway.InternetGatewayId --output text)
aws ec2 attach-internet-gateway \
  --internet-gateway-id $IGW --vpc-id $VPC

aws ec2 modify-subnet-attribute \
  --subnet-id $SUBNET --map-public-ip-on-launch
```

## 4. Launch Instance

```bash
INSTANCE=$(aws ec2 run-instances \
  --image-id $AMI --instance-type t3.small \
  --subnet-id $SUBNET --key-name mykey \
  --query Instances[0].InstanceId --output text)

aws ec2 describe-instances --instance-ids $INSTANCE \
  --query 'Reservations[0].Instances[0].[PrivateIpAddress,PublicIpAddress]'
```

# Troubleshooting

## VPC creation fails

Check OVN services and vpcd daemon:

```bash
sudo systemctl is-active ovn-controller
cat ~/spinifex/logs/vpcd.log
```

## Instances cannot reach each other

Geneve tunnels may not be established:

```bash
sudo ovs-vsctl show | grep -i geneve
sudo ss -ulnp | grep 6081
```

From inside a VM:

```bash
ip addr show
ip route show
```

## Instance has no public IP

1. Check subnet has `MapPublicIpOnLaunch`:
   ```bash
   aws ec2 describe-subnets --subnet-ids $SUBNET \
     --query 'Subnets[0].MapPublicIpOnLaunch'
   ```

2. Check IGW is attached:
   ```bash
   aws ec2 describe-internet-gateways \
     --filters Name=attachment.vpc-id,Values=$VPC
   ```

3. Check external IP pool:
   ```bash
   nats kv get spinifex-external-ipam wan
   ```

4. Check OVN NAT rules:
   ```bash
   sudo ovn-nbctl lr-nat-list vpc-$VPC
   ```

## Public IP not reachable from WAN

1. Verify br-external:
   ```bash
   sudo ovs-vsctl show | grep -A5 br-external
   ```

2. Verify bridge mappings:
   ```bash
   sudo ovs-vsctl get Open_vSwitch . external-ids:ovn-bridge-mappings
   ```

3. Check gateway chassis:
   ```bash
   sudo ovn-nbctl lrp-get-gateway-chassis gw-$VPC
   ```

4. Verify `dnat_and_snat` rule:
   ```bash
   sudo ovn-nbctl lr-nat-list vpc-$VPC | grep dnat_and_snat
   ```

## Security group rules not taking effect

```bash
# Check port is in correct port group
sudo ovn-nbctl pg-get-ports sg-$SG_ID

# Check ACLs
sudo ovn-nbctl acl-list sg-$SG_ID
```
