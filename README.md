# Hive: An Open Source AWS-Compatible Stack for Bare-Metal, Edge, and On-Prem Deployments

Hive developed by [Mulga Defense Corporation](https://mulgadc.com/) is an open source infrastructure platform that brings the core services of AWS—like EC2, VPC, EBS, and S3—to environments where running in the cloud isn't an option. Whether you're deploying to edge sites, private data-centers, or need to operate in low-connectivity or highly contested environments, Hive gives you AWS-style workflows on your own hardware.

## What is Hive?

Hive replicates essential AWS primitives—virtual machines, block volumes, and object storage—using lightweight, self-contained components that are easy to deploy and integrate.

It’s designed for developers and operators who need:

- Cloud-like infrastructure without the cloud
- Minimal dependencies, full control
- Drop-in compatibility with tools like the AWS CLI, SDKs, and Terraform
- A secure cloud environment you control and own the entire hardware, network and software stack

You can run Hive on a few servers in a rack, a field site, or anywhere centralized cloud services aren’t feasible.

## Core Components

### Hive (Compute Service – EC2 Alternative)

Hive is a minimal VM orchestration layer built on top of QEMU, exposing APIs similar to EC2. It manages lifecycle operations like start, stop, and terminate, using QEMU’s QMP interface. Designed to be straightforward and scriptable, Hive lets you launch VMs using the AWS CLI, SDKs, or Terraform—without needing Kubernetes or heavyweight orchestrators. Keep in mind, you can also setup a Kubernetes environment using Hive with underlying instances.

- EC2-like VM management on bare metal
- Launches with cloud-init metadata support
- Works with standard AWS tooling

### Viperblock (Block Storage – EBS Alternative)

[Viperblock](https://github.com/mulgadc/viperblock) is a high-performance, WAL-backed block storage service that replicates volumes across multiple nodes. It’s built for reliability and speed, with support for snapshots, recovery, and direct connection to QEMU instances using NBD or virtio-blk.

- Fast, durable virtual disks
- Replication for resilience
- Exposed over NBD or embedded in VMs
- Supports high performance WAL logs using local NVMe drives to reduce IO traffic to S3.
- In memory read/write block cache for blazing performance.

### Predastore (Object Storage – S3-Compatible)

[Predastore](https://github.com/mulgadc/predastore) is a fully S3-compatible object storage system. It supports the AWS S3 API, including Signature V4 authentication, multipart uploads, and Terraform provisioning. Data is chunked and distributed across nodes using Reed-Solomon erasure coding, making it fault-tolerant and ideal for large-scale or low-bandwidth scenarios.

- S3-compatible API and auth
- Multipart uploads, streaming reads/writes
- Data redundancy with Reed-Solomon encoding

## Key Features

AWS-Compatible Interfaces – Provision infrastructure with awscli, Terraform, or SDKs you already use.

- Designed for Control – Run on your own terms, whether that’s in a datacenter, remote site, or sensitive environment.
- Minimal Dependencies – Each service is standalone and avoids complex orchestration layers.
- Works Offline – No reliance on centralized cloud services or external networks.
- Open Source – Licensed under Apache 2.0. Fork it, extend it, or deploy it as-is.

## Quick Start

See [Installation Guide](INSTALL.md) on how to setup a Hive node to get started.

Then configure your AWS CLI to point to Hive’s endpoints and start launching VMs, buckets, and volumes using familiar commands and Terraform scripts.

## License

Hive is open source under the Apache 2.0 License. You’re free to use, modify, and deploy it—anywhere you need reliable infrastructure without depending on centralized cloud platforms
