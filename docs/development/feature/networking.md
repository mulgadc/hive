# Networking Architecture

This document describes the current networking implementation, its limitations, and the roadmap for production-ready networking with full EC2 metadata service compatibility.

## Current State: SLIRP User-Mode Networking

### Overview

Hive currently uses QEMU's SLIRP (user-mode) networking for VM connectivity. This provides:

- **Zero configuration** — works out of the box without root privileges
- **SSH port forwarding** — `hostfwd=tcp:127.0.0.1:PORT-:22` for development access
- **NAT connectivity** — guests can reach external networks via the host

### Limitations

1. **Performance** — SLIRP has significant overhead compared to TAP/virtio-net
2. **No direct guest access** — VMs are only reachable via port forwarding
3. **169.254.169.254 blocked** — SLIRP rejects link-local addresses in `guestfwd`, preventing standard EC2 metadata service access

### Current Workaround

Since SLIRP rejects `169.254.169.254` for guestfwd (link-local address conflict with SLIRP internals), the metadata service is currently exposed at `10.0.2.100` within the SLIRP subnet:

```
guestfwd=tcp:10.0.2.100:80-cmd:/path/to/hive relay 127.0.0.1:METADATA_PORT
```

Cloud-init is configured via the cidata ISO to query `http://10.0.2.100`:

```yaml
datasource:
  Ec2:
    metadata_urls:
      - http://10.0.2.100
```

**Impact**: AWS SDKs and tools that hardcode `169.254.169.254` will not work inside guests until TAP networking is implemented.

---

## Production Target: TAP + Bridge Networking

### Why TAP Networking?

TAP networking is required for production because:

1. **Full 169.254.169.254 support** — iptables DNAT can redirect metadata traffic
2. **High performance** — virtio-net with TAP has near-native performance
3. **Direct guest access** — VMs can be assigned real/virtual IPs
4. **VPC compatibility** — enables proper subnet, security group, and routing implementation

### How OpenStack Does It

OpenStack's implementation (relevant to Hive's architecture):

1. **Neutron metadata agent** — runs on compute/network nodes
2. **Namespace isolation** — each tenant network has its own namespace
3. **HAProxy in namespace** — listens on `169.254.169.254:80`
4. **iptables DNAT** — redirects metadata traffic to HAProxy
5. **Unix socket forwarding** — HAProxy → metadata agent → Nova API
6. **Shared secret** — prevents metadata spoofing via signed headers

Key insight: The metadata service doesn't need to bind to `169.254.169.254` directly — iptables DNAT redirects traffic from that address to the actual service.

Sources:
- [OpenStack Nova Metadata Service](https://docs.openstack.org/nova/latest/admin/metadata-service.html)
- [OpenStack Neutron Metadata Handling](https://leftasexercise.com/2020/03/30/openstack-neutron-handling-instance-metadata/)
- [OpenStack OVN Metadata API](https://docs.openstack.org/neutron/latest/contributor/internals/ovn/metadata_api.html)

### How Firecracker Does It

Firecracker (AWS's microVM) takes a simpler approach:

- **Built-in metadata service** — handles `169.254.169.254` directly in the VMM
- **User-mode TCP stack** — small TCP implementation in the hypervisor
- **No TAP required for metadata** — traffic intercepted before reaching guest network

This is elegant but requires VMM-level changes. For QEMU, we use the iptables approach.

Sources:
- [Firecracker Documentation](https://firecracker-microvm.github.io/)
- [LWN: The Firecracker VMM](https://lwn.net/Articles/775736/)

---

## Implementation Plan

### Phase 1: TAP Infrastructure (Foundation)

**Goal**: Enable TAP-based networking alongside SLIRP for gradual migration

#### 1.1 TAP Device Management

```go
// Create TAP device for VM
func createTAPDevice(vmID string) (string, error) {
    tapName := fmt.Sprintf("tap-%s", vmID[:8])
    // Use ip tuntap or /dev/net/tun
    // Set device up
    // Return tap name
}
```

#### 1.2 Bridge Setup

Create a bridge per VPC subnet (or use a shared bridge for simple deployments):

```bash
# One-time setup (can be automated in hive admin init)
ip link add br-hive type bridge
ip addr add 10.0.0.1/24 dev br-hive
ip link set br-hive up

# Enable IP forwarding
sysctl -w net.ipv4.ip_forward=1

# NAT for outbound traffic
iptables -t nat -A POSTROUTING -s 10.0.0.0/24 ! -d 10.0.0.0/24 -j MASQUERADE
```

#### 1.3 QEMU TAP Integration

```go
// Replace SLIRP with TAP
netDevValue := fmt.Sprintf("tap,id=net0,ifname=%s,script=no,downscript=no", tapName)
```

#### 1.4 Root Privilege Handling

TAP requires `CAP_NET_ADMIN`. Options:
- Run hive daemon as root (simplest for single-node)
- Use `qemu-bridge-helper` with setuid
- Separate privileged network agent

### Phase 2: Metadata Service with 169.254.169.254

**Goal**: Full EC2 metadata compatibility at the standard address

#### 2.1 iptables DNAT Rule

Per-VM iptables rule to redirect metadata traffic:

```bash
# Redirect 169.254.169.254:80 from VM's tap to metadata service
iptables -t nat -A PREROUTING \
  -i tap-${VM_ID} \
  -d 169.254.169.254 -p tcp --dport 80 \
  -j DNAT --to-destination 127.0.0.1:${METADATA_PORT}
```

#### 2.2 Integration with LaunchInstance

```go
func (d *Daemon) LaunchInstance(instance *vm.VM) error {
    // ... existing code ...

    // Create TAP device
    tapName, err := createTAPDevice(instance.ID)
    if err != nil {
        return fmt.Errorf("failed to create TAP: %w", err)
    }

    // Add to bridge
    if err := addToBridge(tapName, "br-hive"); err != nil {
        return fmt.Errorf("failed to add TAP to bridge: %w", err)
    }

    // Setup metadata DNAT
    if instance.MetadataServerAddress != "" {
        if err := setupMetadataDNAT(tapName, instance.MetadataServerAddress); err != nil {
            slog.Warn("Failed to setup metadata DNAT", "err", err)
        }
    }

    // Use TAP netdev instead of SLIRP
    netDevValue := fmt.Sprintf("tap,id=net0,ifname=%s,script=no,downscript=no", tapName)
    // ...
}
```

#### 2.3 Cleanup on Termination

```go
func (d *Daemon) TerminateInstance(instance *vm.VM) error {
    // Remove iptables DNAT rule
    removeMetadataDNAT(tapName, instance.MetadataServerAddress)

    // Delete TAP device
    deleteTAPDevice(tapName)

    // ... existing termination code ...
}
```

### Phase 3: SSH Access Without Port Forwarding

**Goal**: Maintain development SSH access with TAP networking

#### Option A: Direct Bridge Access (Recommended)

VMs get IPs on the bridge subnet (e.g., `10.0.0.x`). SSH directly:

```bash
ssh -i ~/.ssh/hive-key ec2-user@10.0.0.15
```

**Pros**: Simple, matches production behavior
**Cons**: Requires host routing/bridge configuration

#### Option B: SSH Proxy via Hive CLI

Add a built-in SSH proxy command:

```bash
# Hive manages the connection details
hive ssh i-025a0cf12b0d0a633
```

Implementation:
1. Look up instance's assigned IP from internal state
2. Execute `ssh -i KEY user@IP`

#### Option C: Hybrid Mode (Development)

Keep SLIRP port forwarding for development, TAP for production:

```go
if d.config.NetworkMode == "development" {
    // SLIRP with hostfwd
    netDevValue = fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%s-:22,...", sshPort)
} else {
    // TAP networking
    netDevValue = fmt.Sprintf("tap,id=net0,ifname=%s,script=no,downscript=no", tapName)
}
```

### Phase 4: VPC Integration

**Goal**: Full VPC networking with subnets, security groups, routing

#### 4.1 Per-Subnet Bridges

Each VPC subnet maps to a bridge:

```
subnet-abc123 (10.0.1.0/24) → br-abc123
subnet-def456 (10.0.2.0/24) → br-def456
```

#### 4.2 Security Groups via nftables/iptables

```bash
# Allow SSH from specific CIDR
iptables -A FORWARD -i tap-$VM -p tcp --dport 22 -s 192.168.1.0/24 -j ACCEPT
iptables -A FORWARD -i tap-$VM -j DROP  # Default deny
```

#### 4.3 Route Tables

Linux routing + policy routing for VPC route tables:

```bash
# Custom route table for subnet
ip route add 10.0.0.0/16 via 10.0.1.1 table vpc-123
ip rule add from 10.0.1.0/24 table vpc-123
```

---

## Configuration

### Proposed Config Structure

```yaml
# config.yaml
networking:
  mode: "tap"  # "slirp" (default/dev) or "tap" (production)
  bridge:
    name: "br-hive"
    subnet: "10.0.0.0/24"
    gateway: "10.0.0.1"
  metadata:
    enabled: true
    address: "169.254.169.254"  # Standard EC2 address with TAP mode
```

### Environment Detection

```go
func selectNetworkMode() string {
    // Auto-detect based on capabilities
    if canCreateTAP() && isRoot() {
        return "tap"
    }
    return "slirp"  // Fallback for development
}
```

---

## Migration Path

| Phase | Networking | Metadata Address | SSH Access | Root Required |
|-------|------------|------------------|------------|---------------|
| Current | SLIRP | 10.0.2.100 | Port forwarding | No |
| Phase 1 | TAP (optional) | 10.0.2.100 | Port forward or direct | Yes (for TAP) |
| Phase 2 | TAP | 169.254.169.254 | Direct IP | Yes |
| Phase 3 | TAP | 169.254.169.254 | Direct or `hive ssh` | Yes |
| Phase 4 | TAP + VPC | 169.254.169.254 | VPC routing | Yes |

---

## References

### QEMU Networking
- [QEMU Documentation/Networking](https://wiki.qemu.org/Documentation/Networking)
- [QEMU Advanced Networking - ArchWiki](https://wiki.archlinux.org/title/QEMU/Advanced_networking)
- [Setting up QEMU with TAP](https://gist.github.com/extremecoders-re/e8fd8a67a515fee0c873dcafc81d811c)

### OpenStack Metadata
- [Nova Metadata Service](https://docs.openstack.org/nova/latest/admin/metadata-service.html)
- [Neutron Metadata Handling](https://leftasexercise.com/2020/03/30/openstack-neutron-handling-instance-metadata/)
- [OVN Metadata API](https://docs.openstack.org/neutron/latest/contributor/internals/ovn/metadata_api.html)

### Firecracker
- [Firecracker Documentation](https://firecracker-microvm.github.io/)
- [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor)

### Libvirt/KVM Networking
- [Libvirt NAT Networking](https://wiki.libvirt.org/Networking.html)
- [KVM NAT Port Forwarding](https://blog.wirelessmoves.com/2022/04/bare-metal-cloud-part-2-kvm-and-nat-port-forwarding.html)
- [VM Networking on Linux](https://dev.to/krjakbrjak/setting-up-vm-networking-on-linux-bridges-taps-and-more-2bbc)
