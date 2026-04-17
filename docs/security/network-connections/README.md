---
title: "External Connection Inventory"
description: "Operator inventory of inbound listeners and outbound connections on Spinifex nodes, for CMMC AC.L1-3.1.20 compliance"
category: "Security"
sections:
  - overview
tags:
  - security
  - compliance
  - cmmc
  - network
  - connections
  - boundary
resources:
  - title: "NIST SP 800-171 Rev 3"
    url: "https://csrc.nist.gov/pubs/sp/800/171/r3/final"
  - title: "CMMC Level 1 Self-Assessment Guide v2.0"
    url: "https://dodcio.defense.gov/CMMC/Documentation"
  - title: "NIST SP 800-41 Rev 1 — Guidelines on Firewalls and Firewall Policy"
    url: "https://csrc.nist.gov/pubs/sp/800/41/r1/final"
---

# External Connection Inventory

> Operator inventory of inbound listeners and outbound connections on Spinifex nodes, for CMMC AC.L1-3.1.20 compliance

## Table of Contents

- [Overview](#overview)
- [CMMC Practices Covered](#cmmc-practices-covered)
- [Approach](#approach)
- [1. Inbound Listeners](#1-inbound-listeners)
- [2. Outbound Connections](#2-outbound-connections)
- [3. Cross-Node (Internal) Connections](#3-cross-node-internal-connections)
- [4. Verification and Limiting Controls](#4-verification-and-limiting-controls)
- [5. Configuration Surface](#5-configuration-surface)
- [6. Operator Checklist](#6-operator-checklist)

---

## Overview

**Audience:** Operators deploying Spinifex into environments subject to CMMC Level 1 (or organisations that otherwise require a documented inventory of system connections).

**Scope:** Network connections originated by or terminated at the Spinifex nodes — the Linux hosts running `spinifex-daemon`, `spinifex-awsgw`, `spinifex-nats`, `spinifex-predastore`, `spinifex-viperblock`, `spinifex-vpcd`, `spinifex-ui`, and the OVN control plane. Connections made by guest VMs are the responsibility of the workload owner and are out of scope.

**Boundary definition.** For the purposes of this document:

- **External** means outside the Spinifex cluster's trusted network perimeter — the public internet, the operator's corporate network, tenant users of the AWS API, and guest VMs.
- **Internal** means between Spinifex nodes inside the cluster subnet(s) defined in `spinifex.toml`.

CMMC AC.L1-3.1.20 applies specifically to **external** connections. Internal cluster connections are documented here as well so operators can build an accurate firewall policy, but they are not themselves "external systems" in the sense of the practice.

## CMMC Practices Covered

This guide captures the inventory and verification controls required to meet AC.L1-3.1.20. The related boundary-protection practice SC.L1-3.13.1 is covered by the OVN ACL and security-group enforcement in `vpcd` and is documented separately.

| Practice | Title | Objective |
|----------|-------|-----------|
| AC.L1-3.1.20 | External Connections | [a] Connections to external systems are identified. [b] The use of external systems is identified. [c] Connections to external systems are verified. [d] The use of external systems is verified. [e] Connections to external systems are controlled/limited. [f] The use of external systems is controlled/limited. |

## Approach

Spinifex has a small, enumerable set of network surfaces:

1. **Inbound listeners** — the TCP/UDP ports each node binds. These are the attack surface exposed to whoever can reach the node.
2. **Outbound connections** — the destinations the node's services reach out to. Today this is a short list: peer Spinifex nodes, OS image mirrors, and cloud-init metadata served *by* the cluster (not consumed by it).
3. **Cross-node connections** — inter-node control- and data-plane traffic inside the cluster subnet.

§1 and §2 constitute the connection inventory required by objective [a]/[b]. §4 documents the verification mechanism (TLS, SigV4, token auth, checksum verification) that satisfies objectives [c]/[d]. §5 names the config keys the operator can use to limit a given connection — objectives [e]/[f].

The default Spinifex installation meets [c]/[d]/[e]/[f] for every listed connection; an operator's remaining job is to (i) record this inventory in their system security plan, (ii) constrain it further with host and network firewall rules, and (iii) audit the config surface in §5 periodically.

## 1. Inbound Listeners

All ports below are bound by Spinifex services or their dependencies. "Scope" classifies the intended reach of the listener:

- **External** — reachable by tenant/operator networks. Must be authenticated and TLS-protected.
- **Cluster** — reachable only from peer Spinifex nodes. Operators must restrict with a host or network firewall.
- **Localhost** — bound to `127.0.0.1`. Not reachable off-node.

| Port | Service | Protocol | Scope | Purpose | Auth |
|------|---------|----------|-------|---------|------|
| 9999 | spinifex-awsgw | HTTPS | External | AWS-compatible API (EC2, S3, ELBv2, IAM) — the customer-facing endpoint | AWS SigV4 + TLS (cluster CA) |
| 3000 | spinifex-ui | HTTPS | External | Operator web dashboard | Session cookie + TLS |
| 4432 | Formation server | HTTPS | External (bootstrap only) | Cluster join coordination; active only while a join token is valid | Short-lived bearer token + TLS |
| 4222 | spinifex-nats (client) | NATS + TLS | Cluster | Internal service bus for EC2/EBS/VPC/S3 handlers | Token + TLS |
| 4248 | spinifex-nats (cluster) | NATS + TLS | Cluster | Inter-node NATS federation | Token + TLS |
| 8222 | spinifex-nats (monitoring) | HTTP | Localhost | `varz`/`subsz` metrics consumed by the daemon for health checks | None (loopback only) |
| 8443 | spinifex-predastore | HTTPS | Cluster | S3-compatible object storage for AMIs, snapshots, user objects | AWS SigV4 + TLS |
| 6660–6662 | predastore (Raft DB) | TCP | Cluster | Predastore metadata Raft consensus (3 nodes) | Cluster network only |
| 9991–9993 | predastore (data shards) | TCP | Cluster | Erasure-coded data shard transport | Cluster network only |
| 6641 | OVN Northbound DB | OVSDB/TCP | Cluster | Logical network topology (switches, routers, DHCP) consumed by vpcd | TLS planned — tracked under SC.L2-3.13.8 readiness |
| 6642 | OVN Southbound DB | OVSDB/TCP | Cluster | Chassis / port / MAC binding state | TLS planned — see above |
| 22 | OpenSSH | SSH | External | Operator administration | Key-based auth (operator-managed) |
| socket / dynamic TCP | nbdkit (Viperblock) | NBD | Host-local / cluster | Block device transport for guest EBS volumes | Unix socket by default; TCP only when running remote/DPU |

**Formation port lifecycle.** 4432 is only opened during `spx admin init` / `spx admin join` while a bootstrap token is outstanding. Once the cluster is formed the listener terminates. Document this in the security plan so external reviewers do not flag it as a persistent open port.

**Development-only listeners.** When `dev_networking=true` (development mode), QEMU opens arbitrary host TCP ports for SSH port-forwarding into guest VMs. Production installs (`/etc/spinifex` layout) do not enable this; it must not appear on compliance nodes.

## 2. Outbound Connections

Spinifex nodes initiate a small, fixed set of outbound connections:

| Destination | Purpose | Protocol | Verification |
|-------------|---------|----------|--------------|
| `https://cdimage.debian.org/cdimage/cloud/bookworm/latest/` | Debian 12 cloud image download during `spx admin images import` | HTTPS | TLS (system trust store) + SHA256/SHA512 checksum verification against published manifest |
| `https://cloud-images.ubuntu.com/noble/current/` | Ubuntu 24.04 LTS cloud image download | HTTPS | TLS + checksum verification |
| `https://d2yp8ipz5jfqcw.cloudfront.net/` | Alpine-based system image used for the managed HAProxy load-balancer | HTTPS | TLS + checksum verification |
| `https://install.mulgadc.com/install` | Mulga install telemetry. One-shot POST on `spx admin init` and `spx admin join`. Payload: machine ID (from `/etc/machine-id`), event type, region/AZ, node name, node count, bind IP, Spinifex version, OS/arch. | HTTPS | TLS (system trust store). Opt out with `--no-telemetry` on the `admin` command or set `SPX_NO_TELEMETRY=1` in the environment. |
| Peer nodes, TCP 4248 | NATS cluster route federation | NATS + TLS | Cluster CA-validated TLS + cluster token |
| Peer nodes, TCP 8443 | Predastore S3 reads/writes (AMIs, snapshots, cross-node object access) | HTTPS | Cluster CA-validated TLS + AWS SigV4 |
| Peer nodes, TCP 6641/6642 | OVN NB/SB database reads/writes from `vpcd` | OVSDB/TCP | Cluster-internal; TLS planned |
| Node bind IP, TCP 6660 | Daemon → local Predastore Raft status probe (`/status`) | HTTPS | TLS (cluster CA) |
| Loopback `127.0.0.1:8222` | Daemon → NATS monitoring probe (`/varz`) | HTTP | Loopback only |

**Update checks and metadata.** Spinifex does not check for updates and does not consume a cloud metadata service (`169.254.169.254` is served *by* the cluster to guest VMs, not consumed by nodes). Node software updates are delivered through the operator's OS package channel. The only vendor-operated endpoint the node contacts is the install-telemetry endpoint listed above; compliance deployments that require a closed egress profile should disable it via `SPX_NO_TELEMETRY=1` or `--no-telemetry` and record the opt-out in the system security plan.

**Air-gapped deployments.** The three image URLs above are the only destinations needed for the standard image catalogue. Operators running an air-gapped install mirror these artifacts locally and repoint `utils/images.go`'s catalogue or use `spx admin images import --file` with a pre-staged image file. Telemetry (above) must also be disabled. See [Air-gapped install](https://docs.mulgadc.com/docs/install-airgapped).

## 3. Cross-Node (Internal) Connections

For completeness, the internal control- and data-plane traffic between Spinifex nodes:

| Connection | Port(s) | Encryption | Notes |
|-----------|---------|-----------|-------|
| NATS cluster routes | 4248 | TLS + token | Full mesh between NATS servers |
| Predastore S3 | 8443 | TLS + SigV4 | Cross-node object reads/writes |
| Predastore Raft | 6660–6662 | Cluster network only | Metadata consensus |
| Predastore shards | 9991–9993 | Cluster network only | Erasure-coded data shards |
| OVN NB/SB | 6641/6642 | Cluster network only (TLS planned) | Network control plane |
| OVN tunnels (Geneve) | UDP 6081 | None (tenant traffic encapsulation) | Geneve overlay between chassis; tenant traffic within the cluster subnet |

Operators must place all nodes on a network segment that is **not** routed to tenant/guest VMs or to the internet. Port exposure for Predastore Raft/shards and OVN DBs is cluster-internal and is not expected to be reachable from anywhere else.

## 4. Verification and Limiting Controls

This section maps each connection class to the CMMC AC.L1-3.1.20 objective it satisfies.

### 4.1 Verification (objectives [c], [d])

| Connection | Verification mechanism |
|------------|-----------------------|
| AWS Gateway (9999) inbound | TLS terminated with the per-node cert issued by the cluster CA. All requests authenticated via AWS SigV4 against IAM user access keys. |
| Spinifex UI (3000) inbound | TLS cert + operator session. |
| NATS client/cluster (4222/4248) | Mutually authenticated via TLS against the cluster CA pool; cluster token required on every connect. Steady-state internal TLS dials (NATS, awsgw, predastore, daemon cluster-manager) validate against the cluster CA. |
| Predastore (8443) | TLS + AWS SigV4. |
| Formation (4432) | Short-lived bearer token with configurable TTL (default 30 minutes, set via `spx admin init --token-ttl`). The joining node does not validate the formation server's TLS cert (the server presents an ephemeral self-signed cert that pre-dates trust bootstrap); authenticity of the exchange rests on (a) the operator supplying the correct leader address out-of-band and (b) possession of the bearer token. This is the only production client dial with `InsecureSkipVerify`. |
| Image downloads (outbound HTTPS) | TLS against the system trust store, HTTPS-only (HTTP redirects rejected), and published-manifest SHA256/SHA512 checksum verified before the image is registered. |
| OVN NB/SB (6641/6642) | TLS is not yet enabled; currently relies on cluster-network isolation. Tracked as an L2-readiness item (SC.L2-3.13.8). |

### 4.2 Limiting (objectives [e], [f])

External surface is limited to three listeners by default:

- **9999** — tenant-facing AWS API
- **3000** — operator UI
- **22** — operator SSH

Every other listener is intended for the cluster network only. Operators **must** enforce this with a host firewall (`nftables`/`iptables`/`firewalld`) or an upstream network ACL. A minimal `nftables` policy looks like:

```
# Allow from anywhere
tcp dport { 22, 9999, 3000 } accept

# Allow only from cluster peer subnet(s) — replace with your cluster CIDR
ip saddr 10.0.1.0/24 tcp dport { 4222, 4248, 8443, 6641, 6642, 6660-6662, 9991-9993 } accept
ip saddr 10.0.1.0/24 udp dport 6081 accept

# Default deny
tcp dport 0-65535 drop
udp dport 0-65535 drop
```

The formation port (4432) should be **closed** on nodes outside the bootstrap window. The `spx admin join` process only opens it transiently.

Outbound egress can be limited to the three image-catalogue hostnames (§2) plus any OS package repositories the operator relies on. On air-gapped nodes, block all outbound HTTPS and supply images via `spx admin images import --file`.

## 5. Configuration Surface

Every listener and outbound destination above is controlled by one of these files. Changes require a service restart.

| File | Keys | Controls |
|------|------|----------|
| `/etc/spinifex/spinifex.toml` | `nodes.<node>.awsgw.host`, `nodes.<node>.nats.host`, `nodes.<node>.predastore.host`, `nodes.<node>.daemon.host`, `nodes.<node>.vpcd.ovn_nb_addr`, `nodes.<node>.vpcd.ovn_sb_addr`, `nodes.<node>.daemon.dev_networking` | Listener bind addresses and ports for each Spinifex service on this node; dev-mode QEMU port forwarding. |
| `/etc/spinifex/nats.conf` | `listen`, `cluster.listen`, `cluster.routes`, `http`, `tls`, `cluster.authorization` | NATS client/cluster/monitoring listeners, peer routes, TLS config, cluster token. |
| `/etc/spinifex/predastore.toml` | `host`, `port`, `[[db]].port`, `[[nodes]].port`, `tls.*` | Predastore S3 listener, Raft ports, shard ports, TLS certs. |
| OVN packages (`ovn-central`, `ovn-host`) | `ovn-nb-db`, `ovn-sb-db` connection strings (`ovs-vsctl set open_vswitch …`) | OVN DB bind addresses. |
| Spinifex UI service | Built-in defaults: `host = "0.0.0.0"`, `port = 3000`. No `spinifex.toml` block today; override requires code change or service-level config. | UI listener. |
| `spx admin init` / `spx admin join` CLI flags | `--port`, `--token-ttl`, `--no-telemetry` (or env `SPX_NO_TELEMETRY=1`) | Formation server port, token lifetime, install-telemetry opt-out. |
| `utils/images.go` `AvailableImages` | Image catalogue URLs | Outbound HTTPS destinations for image downloads. Air-gapped operators should change or disable. |

## 6. Operator Checklist

Use this list to confirm a node meets AC.L1-3.1.20 before it is admitted to a production cluster:

- Inventory recorded in the system security plan: inbound listeners (§1), outbound destinations (§2), and cross-node connections (§3) match what is observed on the node (`ss -tlnp`, `ss -unlp`).
- Host firewall enforces the external/cluster/localhost split in §4.2. External surface is limited to 9999, 3000, 22 (and 4432 only during cluster bootstrap).
- Cluster subnet is isolated from tenant guest VM networks and from the public internet.
- Formation port 4432 is closed on nodes that are not actively running a bootstrap token.
- Outbound HTTPS is either restricted to the three image-catalogue hosts in §2 or replaced with an air-gapped image import workflow.
- Install telemetry (`install.mulgadc.com`) is either permitted and recorded in the security plan, or disabled via `SPX_NO_TELEMETRY=1` / `--no-telemetry`.
- OVN NB/SB (6641/6642) exposure is limited to the cluster subnet pending the L2 TLS work.
- SSH (22) is configured to operator-managed keys only; password auth disabled in `sshd_config`.
- Periodic review (at least annually, and after any network topology change) confirms the inventory here still matches the deployed configuration.
