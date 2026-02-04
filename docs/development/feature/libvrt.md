# Libvirt Integration Proposal

This document evaluates migrating from direct QEMU management to libvirt for VM lifecycle management, with a focus on solving the networking privilege problem while maintaining simplicity.

## Problem Statement

Hive currently manages QEMU processes directly. To implement TAP networking (required for `169.254.169.254` metadata support), we need:

1. **TAP device creation** — requires `CAP_NET_ADMIN` or root
2. **Bridge management** — requires root privileges
3. **iptables DNAT rules** — requires root privileges

Running the hive daemon as root is undesirable for security reasons. We need a solution that:

- Maintains least-privilege for the hive daemon
- Simplifies network setup for users
- Works reliably on Ubuntu and Debian
- Doesn't add complex installation steps

---

## Option 1: qemu-bridge-helper (Setuid Approach)

### Overview

QEMU provides `qemu-bridge-helper`, a setuid binary that allows unprivileged users to attach VMs to bridges.

### Availability

```bash
# Ubuntu/Debian — included in qemu-system-common
dpkg -S /usr/lib/qemu/qemu-bridge-helper
# → qemu-system-common: /usr/lib/qemu/qemu-bridge-helper

# Note: It's at /usr/lib/qemu/, not in $PATH
# QEMU invokes it internally — not meant to be run directly
ls -la /usr/lib/qemu/qemu-bridge-helper
# → -rwxr-xr-x 1 root root 687280 ... /usr/lib/qemu/qemu-bridge-helper
```

**Status**: Available on Ubuntu 22.04, 24.04, and Debian 12+ as part of the standard QEMU packages (already a Hive dependency). Located at `/usr/lib/qemu/qemu-bridge-helper` (not in `$PATH` by design).

### Setup Required

```bash
# 1. Create bridge config (one-time, requires root)
sudo mkdir -p /etc/qemu
echo "allow br-hive" | sudo tee /etc/qemu/bridge.conf
sudo chmod 644 /etc/qemu/bridge.conf

# 2. Enable setuid on helper (one-time, requires root)
sudo chmod u+s /usr/lib/qemu/qemu-bridge-helper

# 3. Create the bridge (one-time, requires root)
sudo ip link add br-hive type bridge
sudo ip addr add 10.0.0.1/24 dev br-hive
sudo ip link set br-hive up

# 4. Enable IP forwarding and NAT (one-time, requires root)
sudo sysctl -w net.ipv4.ip_forward=1
sudo iptables -t nat -A POSTROUTING -s 10.0.0.0/24 ! -d 10.0.0.0/24 -j MASQUERADE
```

### QEMU Usage

```bash
# Non-root QEMU process can now use bridge networking
qemu-system-x86_64 ... \
  -netdev bridge,id=net0,br=br-hive \
  -device virtio-net-pci,netdev=net0
```

### Pros

- **No daemon changes** — hive continues to exec QEMU directly
- **Simple concept** — single setuid binary, widely understood
- **Already installed** — part of `qemu-system-common`
- **Low attack surface** — helper only does one thing (add TAP to bridge)

### Cons

- **Manual setup required** — user must run setup commands as root
- **Setuid concerns** — some security policies prohibit setuid binaries
- **Bridge still needs root** — initial bridge creation requires privileges
- **No metadata DNAT** — still need root for iptables rules

### Verdict

Good for bridge attachment, but doesn't solve the metadata DNAT problem. Would still need a privileged component for `169.254.169.254` routing.

---

## Option 2: Libvirt Integration

### Overview

Libvirt is a daemon and API that manages VMs with elevated privileges while exposing a controlled interface to unprivileged clients. It handles:

- VM lifecycle (create, start, stop, migrate)
- Network management (bridges, TAPs, iptables)
- Storage management (pools, volumes)
- Security confinement (SELinux, AppArmor, cgroups)

### Architecture Change

```
Current:
  hive daemon → exec QEMU directly → SLIRP networking

With libvirt:
  hive daemon → libvirt API → libvirtd (root) → QEMU → TAP/bridge networking
```

### Go Libraries

Two options for Go integration:

**1. DigitalOcean go-libvirt (Pure Go)**
```go
import "github.com/digitalocean/go-libvirt"

// Connect to libvirtd
conn, _ := libvirt.NewConnect("qemu:///system")
defer conn.Disconnect()

// Define VM from XML
dom, _ := conn.DomainDefineXML(vmXML)
dom.Create()
```
- Pure Go, no CGo dependencies
- Uses libvirt RPC protocol directly
- Simpler builds, easier cross-compilation

**2. Official libvirt-go (CGo)**
```go
import "libvirt.org/go/libvirt"

conn, _ := libvirt.NewConnect("qemu:///system")
defer conn.Close()

dom, _ := conn.DomainDefineXML(vmXML, 0)
dom.Create()
```
- Full API coverage
- Requires libvirt-dev for building
- Official, well-maintained

**Recommendation**: Start with DigitalOcean's pure Go library for simpler builds.

Sources:
- [go-libvirt (DigitalOcean)](https://github.com/digitalocean/go-libvirt)
- [libvirt-go (Official)](https://libvirt.org/go/libvirt.html)

### Network Configuration

Libvirt manages networks via XML definitions:

```xml
<network>
  <name>hive-network</name>
  <forward mode='nat'/>
  <bridge name='br-hive' stp='on' delay='0'/>
  <ip address='10.0.0.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='10.0.0.10' end='10.0.0.254'/>
    </dhcp>
  </ip>
</network>
```

Create via API:
```go
networkXML := `<network>...</network>`
network, _ := conn.NetworkDefineXML(networkXML)
network.Create()
network.SetAutostart(true)
```

### VM Definition with Bridge Networking

```xml
<domain type='kvm'>
  <name>i-025a0cf12b0d0a633</name>
  <memory unit='MiB'>8192</memory>
  <vcpu>2</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
  </os>
  <devices>
    <disk type='network' device='disk'>
      <driver name='qemu' type='raw'/>
      <source protocol='nbd' name='default'>
        <host name='127.0.0.1' port='12345'/>
      </source>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <source network='hive-network'/>
      <model type='virtio'/>
    </interface>
  </devices>
</domain>
```

### Metadata Service with Libvirt

Libvirt doesn't handle metadata DNAT directly, but provides hooks:

**Option A: Libvirt network hooks**
```bash
# /etc/libvirt/hooks/network
# Called when network starts/stops
# Add iptables DNAT rules here
```

**Option B: QEMU hooks**
```bash
# /etc/libvirt/hooks/qemu
# Called on VM start/stop
# Add per-VM iptables rules
```

**Option C: Separate metadata agent**
Like OpenStack Neutron — a small privileged daemon that manages metadata routing.

### Pros

- **Privilege separation** — libvirtd runs as root, hive runs unprivileged
- **Network management** — bridges, DHCP, NAT handled automatically
- **Security confinement** — SELinux/AppArmor policies for QEMU processes
- **Production proven** — used by OpenStack, oVirt, Proxmox, etc.
- **Migration support** — live migration between hosts
- **Already a dependency** — libvirt packages already in INSTALL.md
- **Consistent interface** — abstracts QEMU version differences
- **Remote management** — can manage VMs on other hosts

### Cons

- **Significant refactor** — VM lifecycle code needs rewrite
- **XML-based config** — verbose domain/network definitions
- **Libvirtd dependency** — another daemon to manage
- **Learning curve** — libvirt concepts (pools, networks, domains)
- **Build complexity** — CGo bindings need libvirt-dev headers
- **Debugging harder** — another layer between hive and QEMU

### Security Benefits

From Red Hat's documentation:

> "The QEMU/libvirt split is extremely important for security. Because the QEMU process handles input from the guest, it is exposed to potentially malicious activity. Libvirt confines QEMU using file permissions, cgroups, and SELinux multi-category security."

This is a strong argument for production deployments.

Sources:
- [Libvirt QEMU Driver](https://libvirt.org/drvqemu.html)
- [KVM Userspace Security](https://www.redhat.com/en/blog/all-you-need-know-about-kvm-userspace)

---

## Option 3: Hybrid Approach

### Overview

Keep direct QEMU management but add a small privileged helper for networking:

```
hive daemon (unprivileged)
    ↓
hive-netd (root, minimal)
    ├── Creates TAP devices
    ├── Attaches to bridges
    ├── Manages iptables DNAT for metadata
    └── Communicates via Unix socket
```

### Implementation

```go
// hive-netd: privileged network daemon
type NetRequest struct {
    Action    string // "create-tap", "delete-tap", "setup-metadata"
    VMID      string
    Bridge    string
    MetaPort  int
}

// Unix socket server
listener, _ := net.Listen("unix", "/run/hive/netd.sock")
```

### Pros

- **Minimal privilege** — only networking code runs as root
- **No major refactor** — hive daemon code mostly unchanged
- **No libvirt dependency** — simpler installation
- **Purpose-built** — does exactly what we need, nothing more

### Cons

- **Custom code** — need to write and maintain the helper
- **Two daemons** — additional process to manage
- **Security surface** — Unix socket needs careful permission handling

---

## Comparison Matrix

| Factor | qemu-bridge-helper | Libvirt | Hybrid (hive-netd) |
|--------|-------------------|---------|-------------------|
| **Privilege separation** | Partial | Full | Full |
| **Metadata DNAT** | ❌ No | Via hooks | ✅ Yes |
| **Installation complexity** | Low | Medium | Medium |
| **Refactor required** | Minimal | Major | Moderate |
| **Production proven** | Limited | Extensive | New |
| **Security confinement** | None | SELinux/AppArmor | Manual |
| **Remote management** | ❌ No | ✅ Yes | ❌ No |
| **Migration support** | ❌ No | ✅ Yes | ❌ No |
| **Build complexity** | None | CGo (optional) | None |

---

## Recommendation

### Short-term (Phase 1-2): qemu-bridge-helper + hive-netd

1. Use `qemu-bridge-helper` for TAP/bridge attachment (already available)
2. Create minimal `hive-netd` for metadata DNAT management
3. Add `hive admin network setup` command to automate initial configuration

**Rationale**: Smallest change, solves immediate `169.254.169.254` problem.

### Long-term (Phase 3+): Evaluate Libvirt

As VPC features mature (subnets, security groups, routing), re-evaluate libvirt:

1. If complexity grows significantly → migrate to libvirt
2. If staying simple → continue with hybrid approach

**Rationale**: Libvirt's benefits (security confinement, migration, remote management) become more valuable as the platform scales.

---

## Installation Impact

### Current INSTALL.md Dependencies

```bash
sudo apt install ... libvirt-daemon-system libvirt-clients libvirt-dev ...
```

Libvirt is already listed as a dependency.

### Additional Setup for TAP Networking

Add to INSTALL.md or create `hive admin network init`:

```bash
# Automated setup command (proposed)
sudo ./bin/hive admin network init --bridge br-hive --subnet 10.0.0.0/24

# This would:
# 1. Create /etc/qemu/bridge.conf if needed
# 2. Set qemu-bridge-helper setuid
# 3. Create and configure br-hive bridge
# 4. Enable IP forwarding
# 5. Add NAT rules
# 6. (Optional) Start hive-netd for metadata routing
```

### Verification

```bash
# Verify network is ready
hive admin network status

# Output:
# Bridge: br-hive (up)
# Subnet: 10.0.0.0/24
# Gateway: 10.0.0.1
# NAT: enabled
# Metadata routing: enabled (hive-netd running)
# qemu-bridge-helper: /usr/lib/qemu/qemu-bridge-helper (setuid)
```

---

## Migration Path

| Phase | Networking | Metadata | Privilege Model |
|-------|------------|----------|-----------------|
| Current | SLIRP | 10.0.2.100 | Unprivileged |
| Phase 1 | qemu-bridge-helper | 10.0.2.100 | Setuid helper |
| Phase 2 | qemu-bridge-helper + hive-netd | 169.254.169.254 | Setuid + daemon |
| Future | Libvirt (optional) | 169.254.169.254 | libvirtd |

---

## OpenVSwitch VPC Integration

### Why OpenVSwitch for VPC?

Hive's VPC implementation requires advanced networking features that go beyond simple Linux bridges:

1. **VXLAN overlays** — multi-tenant isolation across hosts
2. **OpenFlow rules** — programmatic security group enforcement
3. **QoS policies** — bandwidth throttling per VM/tenant
4. **Distributed routing** — L3 routing without hairpinning through a central node
5. **Connection tracking** — stateful firewall rules for security groups

OpenVSwitch (OVS) is the industry standard for software-defined networking in cloud environments, used by OpenStack Neutron, VMware NSX, and AWS's internal networking.

### Architecture with OVS

```
┌─────────────────────────────────────────────────────────────┐
│                         Compute Node                         │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────┐  ┌─────────┐  ┌─────────┐                      │
│  │   VM1   │  │   VM2   │  │   VM3   │                      │
│  │ (VPC-A) │  │ (VPC-A) │  │ (VPC-B) │                      │
│  └────┬────┘  └────┬────┘  └────┬────┘                      │
│       │            │            │                            │
│  ┌────┴────────────┴────────────┴────┐                      │
│  │        br-int (OVS bridge)        │  ← Security groups,  │
│  │    Port tagging, OpenFlow rules   │    ACLs, QoS         │
│  └────────────────┬──────────────────┘                      │
│                   │                                          │
│  ┌────────────────┴──────────────────┐                      │
│  │        br-tun (OVS bridge)        │  ← VXLAN tunnels     │
│  │     VXLAN encap/decap, VNI tags   │    to other nodes    │
│  └────────────────┬──────────────────┘                      │
│                   │                                          │
└───────────────────┼──────────────────────────────────────────┘
                    │ VXLAN (UDP 4789)
                    ▼
            Physical Network
```

### OVS Integration Points

#### 1. VM Port Management

When a VM starts, create an OVS port with appropriate tags:

```go
// Create OVS port for VM
func (d *Daemon) createOVSPort(vmID, subnetID string, mac string) error {
    portName := fmt.Sprintf("tap-%s", vmID[:8])

    // Add port to integration bridge
    cmd := exec.Command("ovs-vsctl", "add-port", "br-int", portName,
        "--", "set", "Interface", portName, "type=internal",
        "--", "set", "Port", portName, fmt.Sprintf("tag=%d", getVLANTag(subnetID)),
        "--", "set", "Interface", portName, fmt.Sprintf("external-ids:vm-id=%s", vmID))

    return cmd.Run()
}
```

#### 2. Security Groups via OpenFlow

Security groups map to OpenFlow rules on br-int:

```bash
# Allow SSH from 10.0.0.0/24 to VM port 5
ovs-ofctl add-flow br-int "priority=100,tcp,in_port=5,nw_src=10.0.0.0/24,tp_dst=22,actions=normal"

# Default deny
ovs-ofctl add-flow br-int "priority=1,in_port=5,actions=drop"
```

#### 3. VPC Isolation via VXLAN

Each VPC gets a unique VXLAN Network Identifier (VNI):

```bash
# Create VXLAN tunnel to remote host
ovs-vsctl add-port br-tun vxlan-192.168.1.20 \
    -- set Interface vxlan-192.168.1.20 type=vxlan \
    options:remote_ip=192.168.1.20 options:key=flow

# OpenFlow rules to map VPC-A (VNI 1000) to VLAN 100
ovs-ofctl add-flow br-tun "priority=100,tun_id=1000,actions=mod_vlan_vid:100,resubmit(,10)"
```

### OVS + Libvirt Integration

Libvirt supports OVS bridges natively:

```xml
<interface type='bridge'>
  <source bridge='br-int'/>
  <virtualport type='openvswitch'>
    <parameters interfaceid='vm-uuid-here'/>
  </virtualport>
  <model type='virtio'/>
</interface>
```

This is the cleanest integration path — libvirt handles VM lifecycle, OVS handles networking.

### OVS Control Plane Options

| Approach | Description | Complexity |
|----------|-------------|------------|
| **Direct ovs-vsctl** | Shell out to OVS commands | Low |
| **OVS-DB Protocol** | JSON-RPC to ovsdb-server | Medium |
| **OpenFlow Controller** | Custom SDN controller | High |
| **OVN (OVS Network)** | Distributed OVS control plane | High |

**Recommendation**: Start with direct `ovs-vsctl` commands, migrate to OVN when multi-node VPC routing is needed.

### Metadata Service with OVS

OVS can intercept metadata traffic without iptables DNAT:

```bash
# Redirect 169.254.169.254 to local metadata service
ovs-ofctl add-flow br-int \
    "priority=200,ip,nw_dst=169.254.169.254,tcp,tp_dst=80,actions=mod_nw_dst:10.0.0.1,mod_tp_dst:8775,normal"
```

This is cleaner than iptables and works per-bridge rather than globally.

---

## Option 4: NVIDIA BlueField DPU (Commercial)

### Overview

NVIDIA BlueField Data Processing Units (DPUs) are SmartNICs that offload networking, storage, and security to dedicated ARM cores on the NIC itself. This is the architecture used by AWS Nitro, Azure Accelerated Networking, and GCP's custom NICs.

**Note**: This is planned for the commercial version of Hive, targeting enterprise customers with specific performance and isolation requirements.

### What is a DPU?

```
┌────────────────────────────────────────────────────────────────────┐
│                    BlueField DPU Architecture                       │
├────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    Host CPU (x86)                            │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐                      │   │
│  │  │   VM1   │  │   VM2   │  │   VM3   │   (Customer VMs)     │   │
│  │  └────┬────┘  └────┬────┘  └────┬────┘                      │   │
│  │       │            │            │                            │   │
│  │       └────────────┼────────────┘                            │   │
│  │                    │ VirtIO / SR-IOV                         │   │
│  └────────────────────┼────────────────────────────────────────┘   │
│                       │                                             │
│  ┌────────────────────┼────────────────────────────────────────┐   │
│  │                    ▼        BlueField DPU                    │   │
│  │  ┌──────────────────────────────────────────────────────┐   │   │
│  │  │                ARM Cores (8-16 A78)                   │   │   │
│  │  │  ┌────────────┐  ┌────────────┐  ┌────────────┐      │   │   │
│  │  │  │    OVS     │  │  IPsec/TLS │  │  Metadata  │      │   │   │
│  │  │  │  Datapath  │  │   Offload  │  │   Service  │      │   │   │
│  │  │  └────────────┘  └────────────┘  └────────────┘      │   │   │
│  │  └──────────────────────────────────────────────────────┘   │   │
│  │                                                              │   │
│  │  ┌──────────────────────────────────────────────────────┐   │   │
│  │  │           Hardware Accelerators (ASIC)                │   │   │
│  │  │  • ConnectX-7 (200Gbps)    • Crypto Engine           │   │   │
│  │  │  • VXLAN/Geneve offload   • Regex/DPI Engine         │   │   │
│  │  │  • SR-IOV (1000+ VFs)     • Storage (NVMe-oF, iSCSI) │   │   │
│  │  └──────────────────────────────────────────────────────┘   │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                       │                                             │
│                       ▼                                             │
│              Physical Network (25G/100G/200G)                       │
└────────────────────────────────────────────────────────────────────┘
```

### Why DPU for Hive Commercial?

| Benefit | Description |
|---------|-------------|
| **Complete isolation** | Host CPU never sees tenant network traffic — packets handled entirely on DPU |
| **Wire-speed performance** | Hardware VXLAN, crypto, and flow processing at line rate |
| **Security boundary** | DPU runs its own OS — host compromise doesn't expose networking |
| **Consistent performance** | No CPU steal for networking — dedicated cores on DPU |
| **Advanced features** | IPsec encryption, hardware firewall, DDoS mitigation |
| **Multi-tenant scale** | 1000+ SR-IOV VFs per DPU, each isolated |

### AWS Nitro Comparison

AWS Nitro is essentially a custom DPU. Hive with BlueField provides a similar architecture:

| Component | AWS Nitro | Hive + BlueField |
|-----------|-----------|------------------|
| Network virtualization | Nitro Card | BlueField DPU |
| Storage virtualization | Nitro Card | BlueField + NVMe-oF |
| Security monitoring | Nitro Security Chip | BlueField ARM cores |
| Hypervisor | Nitro Hypervisor (KVM-based) | QEMU/KVM on host |
| Instance metadata | Nitro Card | DPU-hosted service |

### DPU Architecture for Hive

#### Control Plane

```
┌─────────────────────────────────────────────────────────────┐
│                    Hive Control Plane                        │
│                                                              │
│  ┌──────────────┐        ┌──────────────┐                   │
│  │ Hive Gateway │───────▶│ NATS Cluster │                   │
│  └──────────────┘        └──────┬───────┘                   │
│                                 │                            │
│           ┌─────────────────────┼─────────────────────┐     │
│           │                     │                     │     │
│           ▼                     ▼                     ▼     │
│  ┌────────────────┐  ┌────────────────┐  ┌────────────────┐ │
│  │  DPU Agent     │  │  DPU Agent     │  │  DPU Agent     │ │
│  │  (Node 1)      │  │  (Node 2)      │  │  (Node 3)      │ │
│  └────────┬───────┘  └────────┬───────┘  └────────┬───────┘ │
│           │                   │                   │         │
└───────────┼───────────────────┼───────────────────┼─────────┘
            │                   │                   │
            ▼                   ▼                   ▼
      BlueField DPU       BlueField DPU       BlueField DPU
```

The DPU Agent runs on the DPU's ARM cores and:
- Receives VM network configuration via NATS
- Programs OVS-DOCA flows (DPU-accelerated OVS)
- Manages SR-IOV VF allocation
- Hosts the metadata service for that node

#### Data Plane

```
VM ─▶ VirtIO/SR-IOV ─▶ DPU (OVS-DOCA) ─▶ VXLAN Encap ─▶ Network
                            │
                            ├── Security group check (hardware)
                            ├── QoS policing (hardware)
                            └── Flow tracking (hardware)
```

All packet processing happens in DPU hardware — zero host CPU involvement.

### BlueField-Specific Features

#### OVS-DOCA (Hardware OVS)

NVIDIA DOCA SDK provides hardware-accelerated OVS:

```bash
# On the DPU, OVS flows are offloaded to hardware
ovs-vsctl set Open_vSwitch . other_config:hw-offload=true
ovs-vsctl set Open_vSwitch . other_config:tc-policy=none

# Flows are automatically offloaded to ConnectX ASIC
ovs-appctl dpctl/dump-flows -m type=offloaded
```

#### SR-IOV for VMs

Each VM gets a dedicated Virtual Function (VF) for near-native performance:

```xml
<!-- Libvirt domain with SR-IOV VF -->
<interface type='hostdev' managed='yes'>
  <source>
    <address type='pci' domain='0x0000' bus='0x3b' slot='0x00' function='0x1'/>
  </source>
  <mac address='fa:16:3e:xx:xx:xx'/>
</interface>
```

#### Hardware Encryption

BlueField offloads IPsec/TLS encryption:

```bash
# Configure IPsec offload on DPU
ip xfrm state add src 10.0.0.1 dst 10.0.0.2 proto esp spi 0x1234 \
    mode tunnel enc "aes-gcm-esp" 0x... offload dev enp3s0f0np0 dir out
```

VPC traffic can be encrypted at wire speed without CPU overhead.

### Best Practices

#### 1. Separation of Concerns

- **Host**: Runs only VMs and minimal hive daemon
- **DPU**: Runs all networking, security, and metadata services
- **Control plane**: NATS messages configure DPU agents

#### 2. Failure Isolation

- DPU failure doesn't crash host VMs (SR-IOV keeps working)
- Host compromise can't access DPU networking code
- Each tenant's flows are isolated in hardware

#### 3. Performance Tuning

```bash
# Increase VF count (up to 1024 per BlueField-3)
echo 256 > /sys/class/net/enp3s0f0np0/device/sriov_numvfs

# Enable representor ports for OVS integration
ovs-vsctl add-port br-int pf0hpf -- set Interface pf0hpf type=dpdk
ovs-vsctl add-port br-int pf0vf0 -- set Interface pf0vf0 type=dpdk
```

#### 4. Metadata Service on DPU

Host the metadata service on the DPU's ARM cores:

```
Guest VM ─▶ 169.254.169.254 ─▶ DPU ─▶ metadata-service (on DPU ARM)
```

This provides:
- Complete isolation from host
- No iptables DNAT needed
- Wire-speed metadata responses
- Secure IMDSv2 enforcement in hardware

### Deployment Model

| Deployment | DPU | Use Case |
|------------|-----|----------|
| **Community Hive** | None | Development, small deployments, cost-sensitive |
| **Hive Enterprise** | BlueField-2/3 | Production multi-tenant, security-sensitive |
| **Hive HPC** | BlueField-3 + InfiniBand | High-performance computing, AI/ML workloads |

### Cost Considerations

- **BlueField-2**: ~$1,500-2,500 per NIC
- **BlueField-3**: ~$3,000-5,000 per NIC
- ROI: Replaces separate firewall, load balancer, encryption appliances

For customers with 50+ nodes, the TCO savings from consolidation typically offset DPU cost.

---

## Option 5: Bare Metal Networking (No DPU)

### Overview

Not all deployments need or can afford DPUs. This section covers production-grade networking using only standard Linux networking and OVS on the host CPU.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Compute Node (No DPU)                     │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────────────────────────────────────────┐│
│  │                    Host CPU (x86)                        ││
│  │                                                          ││
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐                  ││
│  │  │   VM1   │  │   VM2   │  │   VM3   │                  ││
│  │  └────┬────┘  └────┬────┘  └────┬────┘                  ││
│  │       │ TAP        │ TAP        │ TAP                    ││
│  │       └────────────┼────────────┘                        ││
│  │                    ▼                                     ││
│  │  ┌──────────────────────────────────────────────────┐   ││
│  │  │              OVS (br-int)                         │   ││
│  │  │  • Security groups (OpenFlow)                    │   ││
│  │  │  • VLAN tagging per subnet                       │   ││
│  │  │  • QoS rate limiting                             │   ││
│  │  └────────────────────┬─────────────────────────────┘   ││
│  │                       │                                  ││
│  │  ┌────────────────────┴─────────────────────────────┐   ││
│  │  │              OVS (br-tun)                         │   ││
│  │  │  • VXLAN encapsulation                           │   ││
│  │  │  • Tunnel endpoint management                    │   ││
│  │  └────────────────────┬─────────────────────────────┘   ││
│  │                       │                                  ││
│  │  ┌────────────────────┴─────────────────────────────┐   ││
│  │  │              OVS (br-ex)                          │   ││
│  │  │  • Physical NIC attachment                       │   ││
│  │  │  • External network access                       │   ││
│  │  └────────────────────┬─────────────────────────────┘   ││
│  │                       │                                  ││
│  └───────────────────────┼──────────────────────────────────┘│
│                          │                                   │
│                    Physical NIC                              │
│                  (10G/25G/40G/100G)                          │
└──────────────────────────┼───────────────────────────────────┘
                           │
                    Physical Network
```

### Performance Characteristics

| Workload | Host CPU OVS | DPU OVS-DOCA |
|----------|--------------|--------------|
| Packet forwarding (64B) | ~5 Mpps | ~100+ Mpps |
| VXLAN encap throughput | ~25 Gbps | Line rate |
| Security group eval | CPU cycles | Zero |
| Encryption (IPsec) | 1-5 Gbps | Line rate |

For most workloads (web servers, databases, applications), host CPU OVS is sufficient.

### When Bare Metal is Appropriate

- **Development and testing** — no need for DPU complexity
- **Small deployments** — <10 nodes, cost-sensitive
- **Low network I/O** — VMs don't saturate NICs
- **Trusted tenants** — security isolation less critical
- **Edge deployments** — space/power constrained

### Security Without DPU

Without hardware isolation, implement defense-in-depth:

#### 1. OVS Firewall Driver

```bash
# Enable OVS native firewall (stateful, connection tracking)
ovs-vsctl set bridge br-int protocols=OpenFlow13
ovs-vsctl set bridge br-int other-config:disable-in-band=true

# Security group rule: allow SSH
ovs-ofctl add-flow br-int "table=0,priority=100,ct_state=-trk,tcp,tp_dst=22,actions=ct(table=1)"
ovs-ofctl add-flow br-int "table=1,priority=100,ct_state=+trk+new,actions=ct(commit),normal"
ovs-ofctl add-flow br-int "table=1,priority=100,ct_state=+trk+est,actions=normal"
```

#### 2. Network Namespaces

Isolate control plane from data plane:

```bash
# Metadata service in isolated namespace
ip netns add hive-meta
ip link add veth-meta type veth peer name veth-meta-br
ip link set veth-meta netns hive-meta
ip netns exec hive-meta ip addr add 169.254.169.254/32 dev veth-meta
ip netns exec hive-meta ./hive-metadata-service
```

#### 3. AppArmor/SELinux

Confine QEMU processes:

```bash
# AppArmor profile for QEMU
profile qemu-vm {
  # Allow only necessary capabilities
  capability net_admin,

  # Restrict network access to TAP devices
  network inet stream,
  /dev/net/tun rw,

  # Restrict file access
  /var/lib/hive/images/** r,
  deny /etc/shadow r,
}
```

### Privilege Model for Bare Metal

Two approaches for running without root:

#### Approach A: hive-netd Helper (Recommended)

```
┌─────────────────────────────────────────────────────────────┐
│                                                              │
│  hive daemon (unprivileged, user: hive)                     │
│      │                                                       │
│      │ Unix socket                                           │
│      ▼                                                       │
│  hive-netd (root, minimal)                                  │
│      │                                                       │
│      ├── Creates TAP devices                                │
│      ├── Configures OVS ports                               │
│      ├── Manages iptables/OpenFlow rules                    │
│      └── Sets up 169.254.169.254 routing                    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### Approach B: Capability-Based

Grant specific capabilities without full root:

```bash
# Grant network capabilities to hive daemon
setcap cap_net_admin,cap_net_raw+eip /usr/local/bin/hive

# Still need root for OVS management
# Solution: Pre-create OVS bridges, grant access via groups
```

### OVS Tuning for Bare Metal

#### CPU Pinning

Dedicate CPU cores to OVS datapath:

```bash
# Pin OVS PMD threads to cores 2-5
ovs-vsctl set Open_vSwitch . other_config:pmd-cpu-mask=0x3c

# Isolate these cores from kernel scheduling
# /etc/default/grub: GRUB_CMDLINE_LINUX="isolcpus=2-5"
```

#### DPDK Integration

For higher performance without DPU:

```bash
# Enable DPDK in OVS
ovs-vsctl set Open_vSwitch . other_config:dpdk-init=true
ovs-vsctl set Open_vSwitch . other_config:dpdk-lcore-mask=0x3
ovs-vsctl set Open_vSwitch . other_config:dpdk-socket-mem=1024

# Add DPDK port
ovs-vsctl add-port br-ex dpdk0 -- set Interface dpdk0 type=dpdk \
    options:dpdk-devargs=0000:3b:00.0
```

DPDK can achieve 15-20 Mpps on modern CPUs — 3-4x improvement over kernel datapath.

### Comparison: Bare Metal vs DPU

| Factor | Bare Metal (OVS) | DPU (BlueField) |
|--------|------------------|-----------------|
| **Cost per node** | $0 (software only) | $1,500-5,000 |
| **Max throughput** | 25-40 Gbps | Line rate (200 Gbps) |
| **CPU overhead** | 5-20% of cores | Zero |
| **Security isolation** | Software-based | Hardware-based |
| **Crypto offload** | No (CPU) | Yes (hardware) |
| **Maintenance** | OVS upgrades | Firmware + OVS |
| **Failure domain** | Host-wide | DPU isolated |
| **Setup complexity** | Medium | High |

### Migration Path: Bare Metal → DPU

Design for portability:

```go
// Network driver interface
type NetworkDriver interface {
    CreatePort(vmID string, subnet *Subnet) (*Port, error)
    DeletePort(vmID string) error
    ApplySecurityGroup(portID string, rules []SecurityRule) error
    SetupMetadataRoute(vmIP net.IP, metadataAddr string) error
}

// Implementations
type OVSDriver struct { ... }      // Bare metal
type DOCADriver struct { ... }     // BlueField DPU
type LibvirtDriver struct { ... }  // Libvirt-managed
```

This allows swapping networking backends without changing Hive's core logic.

---

## Updated Recommendation

### Deployment Matrix

| Deployment Type | Networking Stack | Privilege Model |
|-----------------|------------------|-----------------|
| **Development** | SLIRP | Unprivileged |
| **Community/Small** | qemu-bridge-helper + OVS | Setuid + hive-netd |
| **Production** | OVS + TAP | hive-netd (root) |
| **Enterprise** | BlueField DPU + OVS-DOCA | DPU Agent |
| **Hyperscale** | BlueField-3 + OVN | Distributed control |

### Phase Implementation

| Phase | Networking | When to Implement |
|-------|------------|-------------------|
| 1 | SLIRP (current) | ✅ Done |
| 2 | OVS + TAP + hive-netd | VPC alpha |
| 3 | OVS-DPDK (optional) | Performance-critical deployments |
| 4 | BlueField DPU | Commercial enterprise customers |

### Decision Criteria

**Use Bare Metal (OVS) when:**
- Total cluster size < 50 nodes
- Network I/O < 25 Gbps per node
- Cost is primary concern
- Development or test environment

**Use DPU (BlueField) when:**
- Multi-tenant production environment
- Security isolation is critical
- Network I/O > 25 Gbps per node
- Hardware crypto required
- Regulatory compliance (financial, healthcare)

---

## References

- [Libvirt QEMU/KVM Driver](https://libvirt.org/drvqemu.html)
- [Libvirt Go Bindings](https://libvirt.org/golang.html)
- [go-libvirt (DigitalOcean)](https://github.com/digitalocean/go-libvirt)
- [QEMU Bridge Helper](https://wiki.qemu.org/Documentation/Networking#Tap)
- [KVM Userspace Security (Red Hat)](https://www.redhat.com/en/blog/all-you-need-know-about-kvm-userspace)
- [Libvirt vs QEMU Comparison](https://stackshare.io/stackups/libvirt-vs-qemu)

### OpenVSwitch
- [OVS Documentation](https://docs.openvswitch.org/)
- [OVN Architecture](https://docs.ovn.org/en/latest/ref/ovn-architecture.7.html)
- [OVS with DPDK](https://docs.openvswitch.org/en/latest/intro/install/dpdk/)
- [OVS OpenFlow Tutorial](https://docs.openvswitch.org/en/latest/tutorials/ovs-conntrack/)

### NVIDIA BlueField
- [BlueField DPU Overview](https://www.nvidia.com/en-us/networking/products/data-processing-unit/)
- [DOCA SDK Documentation](https://docs.nvidia.com/doca/sdk/)
- [OVS-DOCA Deployment Guide](https://docs.nvidia.com/doca/sdk/ovs-doca-deployment-guide/)
- [BlueField Network Operator](https://docs.nvidia.com/networking/display/BlueFieldDPUOSLatest)

### AWS Nitro (Reference Architecture)
- [AWS Nitro System](https://aws.amazon.com/ec2/nitro/)
- [The Security Design of the AWS Nitro System (PDF)](https://docs.aws.amazon.com/pdfs/whitepapers/latest/security-design-of-aws-nitro-system/security-design-of-aws-nitro-system.pdf)
- [re:Invent 2018: Powering Next-Gen EC2 Instances](https://www.youtube.com/watch?v=e8DVmwj3OEs)
