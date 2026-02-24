# Hive Installation

Notes for development environment installation.

## Dependencies

The currently supported operating systems that are validated and tested include:

- Ubuntu 22.04
- Ubuntu 24.04
- Ubuntu 25.10
- Debian 12.13

For the development preview, please use one of the supported versions above.

## Download

Create the base directory for the Hive development environment.

```bash
mkdir -p ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
```

### Quick Install

To bootstrap all dependencies of Hive in one step (QEMU, Go, AWS CLI, OVN/OVS):

```bash
sudo make -C hive quickinstall
```

Ensure Go is available in your `PATH` if not previously installed:

```bash
export PATH=$PATH:/usr/local/go/bin/
```

### Manual Install

Alternatively, run the following steps to manually set up the required dependencies.

```bash
sudo add-apt-repository universe
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system libvirt-clients libvirt-dev make gcc unzip xz-utils file ovn-central ovn-host openvswitch-switch
```

**Note:** OVN and Open vSwitch are required for VPC networking (virtual switches, routers, DHCP, Geneve overlay). The packages are installed above, but the setup and configuration step (`setup-ovn.sh`) is covered later — see the [Setup OVN](#setup-ovn) section below.

Ensure the Go toolkit is installed for version 1.26.0 or higher. Recommended to install the latest directly from [https://go.dev/dl/](https://go.dev/dl/).

Confirm Go is correctly installed, and set in your $PATH.

```bash
go version
```

Hive provides AWS API/SDK layer functionality, which requires the AWS CLI tool to be installed to interface with the system.

```bash
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install
```

Confirm awscli version > 2.0 is installed.

[https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)

```bash
aws --version
```

## Build

Setup the dev environment and package dependencies on [viperblock](https://github.com/mulgadc/viperblock/) and [predastore](https://github.com/mulgadc/predastore/).

```bash
cd hive
./scripts/clone-deps.sh    # Clone viperblock + predastore repositories
./scripts/dev-setup.sh     # Setup complete development environment
```

Once complete, confirm `./bin/hive` exists and is executable.

# Single Node Installation

For rapid development and testing, `hive` can be installed locally as a single node instance. Follow the instructions below for a complete working environment. For multi node installation, skip to [Multi-Node Installation](#multi-node-installation)

## Setup OVN

OVN provides the virtual networking layer for VPC instances. This step configures OVS bridges, starts the OVN controller, and enables Geneve tunnel support. For a single-node install, run the setup script with `--management` to start all OVN services locally:

```bash
./scripts/setup-ovn.sh --management
```

This creates the `br-int` integration bridge, starts `ovn-controller`, starts the OVN central databases (NB DB + SB DB), configures Geneve tunnel endpoints, enables IP forwarding, and creates a sudoers rule so the Hive daemon can manage tap devices and OVS ports without running as root. Hive will not start without this step.

## Init

When running Hive for the first time, run the init function to create the default directories for data, config files and layout required.

Example single node installation to get started, this will create a new region `ap-southeast-2` and availability zone `ap-southeast-2a` on your local instance:

```bash
./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

## Trust CA Certificate (Required)

Hive generates a local Certificate Authority (CA) during initialization. For AWS CLI and other tools to trust Hive services over HTTPS, you must add the CA certificate to your system's trust store.

```bash
sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates
```

Verify the certificate was added:

```bash
ls -la /usr/local/share/ca-certificates/hive-ca.crt
```

Next, set the AWS profile to use `hive` which points to the local environment.

```bash
export AWS_PROFILE=hive
```

## Launch Services

Start the core services for development.

```bash
./scripts/start-dev.sh
```

## Create SSH Key

For first install, create or import an existing key pair which can be used to launch EC2 instances.

### Import Existing Key

Import an existing key pair, replace `~/.ssh/id_rsa.pub` with your specified key.

```bash
aws ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

If no key exists, generate one using `ssh-keygen` and repeat the command above.

```bash
ssh-keygen -t rsa
```

### Create New Key

Alternatively, create a new key pair using the AWS CLI tool and store the JSON output of the AWS SDK using the `jq` command.

```bash
aws ec2 create-key-pair \
  --key-name hive-key \
| jq -r '.KeyMaterial | rtrimstr("\n")' > ~/.ssh/hive-key
```

Update permissions for the key for SSH to accept reading the file.

```bash
chmod 600 ~/.ssh/hive-key
```

Next, generate a public key from the specified private key pair.

```bash
ssh-keygen -y -f ~/.ssh/hive-key > ~/.ssh/hive-key.pub
```

### Verify New Key

```bash
aws ec2 describe-key-pairs
```

```json
{
  "KeyPairs": [
    {
      "KeyPairId": "key-3caa0a34f53d12a3",
      "KeyType": "ed25519",
      "Tags": [],
      "CreateTime": "2025-10-28T13:39:23.458000+00:00",
      "KeyName": "hive-key",
      "KeyFingerprint": "SHA256:/g/A5OkeZeSydz9WUErXYVdCt00b0VbfN6RLn2YVFAY"
    }
  ]
}
```

## Create AMI Template

The Hive CLI tool offers 2 ways of importing an image. You can use automatic image importing, and select from our range of images. Alternatively, provide your own image source file (e.g image.tar.gz, image.gz, image.tar.xz) and the tool will automatically extract and upload the raw OS image as an AMI (after validating the image contains UEFI/BIOS boot capability).

**Note:** When downloading OS images, use platforms that support the `cloud-init` feature to automatically bootstrap when using the Hive EC2 functionality to access SSH and networking services.

### Automatic Image Import

Discover available images to automatically download and install. This will pull the images from the distro official mirror and simplify the process to bootstrap a Hive installation with AMIs for common operating systems.

```bash
./bin/hive admin images list
```

```bash
NAME                 | DISTRO | VERSION | ARCH   | BOOT
debian-12-arm64      | debian | 12      | arm64  | bios
debian-12-x86_64     | debian | 12      | x86_64 | bios
ubuntu-24.04-arm64   | ubuntu | 24.04   | arm64  | bios
ubuntu-24.04-x86_64  | ubuntu | 24.04   | x86_64 | bios
```

Next, choose the image you would like to import as an AMI. Choose the appropriate `x86_64` or `arm64` architecture depending on your platform.

```bash
./bin/hive admin images import --name debian-12-arm64
```

### Manual Image Import

Using this method you can import any OS disk image. For example, download the Debian 12 image from the repository [https://cloud.debian.org/images/cloud/bookworm/latest/](https://cloud.debian.org/images/cloud/bookworm/latest/).

Download the image:

```bash
wget https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-arm64.tar.xz -O ~/debian-12-genericcloud-arm64.tar.xz
```

Import as an AMI to the backend store:

```bash
./bin/hive admin images import --file ~/debian-12-genericcloud-arm64.tar.xz --arch arm64 --distro debian --version 12
```

### Verify Image Import

Export the AMI.

```bash
export HIVE_AMI="ami-XXX"
```

Next, verify available disk images to confirm the import was successful.

```bash
aws ec2 describe-images --image-ids $HIVE_AMI
```

## Create VPC and Subnet

Every EC2 instance runs inside a VPC (Virtual Private Cloud) with an isolated virtual network. Hive creates a default VPC automatically during initialization, but you can also create your own.

### Create a VPC

```bash
aws ec2 create-vpc --cidr-block 10.200.0.0/16
```

```json
{
  "Vpc": {
    "VpcId": "vpc-1035bd70d9bc10b06",
    "CidrBlock": "10.200.0.0/16",
    "State": "available",
    "IsDefault": false
  }
}
```

Export the VPC ID:

```bash
export HIVE_VPC="vpc-XXX"
```

### Create a Subnet

Create a subnet within the VPC. All instances launched into this subnet will receive a private IP address from its CIDR range via DHCP.

```bash
aws ec2 create-subnet --vpc-id $HIVE_VPC --cidr-block 10.200.1.0/24
```

```json
{
  "Subnet": {
    "SubnetId": "subnet-6e7f829e3a4b1c5d0",
    "VpcId": "vpc-1035bd70d9bc10b06",
    "CidrBlock": "10.200.1.0/24",
    "AvailableIpAddressCount": 249,
    "State": "available"
  }
}
```

Export the Subnet ID — this is required when launching instances:

```bash
export HIVE_SUBNET="subnet-XXX"
```

### Verify VPC Networking

Confirm the VPC and subnet were created:

```bash
aws ec2 describe-vpcs
aws ec2 describe-subnets
```

Behind the scenes, Hive's `vpcd` service translates these into OVN topology: the VPC becomes a logical router, the subnet becomes a logical switch with DHCP options, and each instance launched into the subnet gets an OVN port with automatic IP assignment.

## Run Instance

Once Hive is successfully installed and bootstrapped with a system AMI, SSH keys, and a VPC subnet, proceed to run an instance.

### Query Instance Types

Depending on the host platform Hive is installed, different compute instances will be available.

Query available instance types and choose an instance type available on your host:

```bash
aws ec2 describe-instance-types
```

```json
{
  "InstanceTypes": [
    {
      "InstanceType": "t3.small",
      "CurrentGeneration": true,
      "SupportedRootDeviceTypes": [
          "ebs"
      ],
      "SupportedVirtualizationTypes": [
          "hvm"
      ],
      "Hypervisor": "kvm",
      "ProcessorInfo": {
          "SupportedArchitectures": [
              "x86_64"
          ]
      },
      "VCpuInfo": {
          "DefaultVCpus": 2
      },
      "MemoryInfo": {
          "SizeInMiB": 2048
      },
      "BurstablePerformanceSupported": false
  },
  ]
}
```

Export the instance-type:

```sh
export HIVE_INSTANCE="t3.small"
```

### Run Instance

Next, launch a new instance. The `--subnet-id` places the instance into your VPC subnet, where it will receive a private IP address via DHCP. The `hive-key` is the SSH key specified in the previous stage.

```bash
aws ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --subnet-id $HIVE_SUBNET \
  --count 1
```

A sample response is below from the `RunInstance` request. Note the `InstanceId` and `PrivateIpAddress` — the instance has been assigned an IP from the subnet's CIDR range:

```json
{
  "ReservationId": "r-f101157331e261a68",
  "OwnerId": "123456789012",
  "Instances": [
    {
      "InstanceId": "i-36765eb0c6609e4d2",
      "ImageId": "ami-15c3dcfe607460f15",
      "State": {
        "Code": 0,
        "Name": "pending"
      },
      "PrivateIpAddress": "10.200.1.5",
      "SubnetId": "subnet-6e7f829e3a4b1c5d0",
      "VpcId": "vpc-1035bd70d9bc10b06",
      "KeyName": "hive-key",
      "InstanceType": "t3.micro",
      "LaunchTime": "2025-11-12T13:07:47.548000+00:00",
      "NetworkInterfaces": [
        {
          "NetworkInterfaceId": "eni-a1b2c3d4e5f6g7h8i",
          "PrivateIpAddress": "10.200.1.5",
          "MacAddress": "02:00:00:1a:2b:3c",
          "SubnetId": "subnet-6e7f829e3a4b1c5d0",
          "VpcId": "vpc-1035bd70d9bc10b06",
          "Status": "in-use",
          "Attachment": {
            "DeviceIndex": 0,
            "Status": "attached"
          }
        }
      ]
    }
  ]
}
```

Export the instance ID.

```bash
export INSTANCE_ID="i-XXX"
```

### Verify Instance

Next, validate the running instance is ready.

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

Confirm the `State.Name` attribute is set as `running` and the instance has a `PrivateIpAddress` assigned from the subnet.

```json
{
  "Reservations": [
    {
      "ReservationId": "r-f101157331e261a68",
      "OwnerId": "123456789012",
      "Instances": [
        {
          "InstanceId": "i-36765eb0c6609e4d2",
          "ImageId": "ami-15c3dcfe607460f15",
          "State": {
            "Code": 16,
            "Name": "running"
          },
          "PrivateIpAddress": "10.200.1.5",
          "SubnetId": "subnet-6e7f829e3a4b1c5d0",
          "VpcId": "vpc-1035bd70d9bc10b06",
          "KeyName": "hive-key",
          "InstanceType": "t3.micro",
          "LaunchTime": "2025-11-12T13:07:47.548000+00:00"
        }
      ]
    }
  ]
}
```

## SSH Connection (development)

For a Hive development environment (toggled off for production), a local SSH port forwarding will be active to connect directly to the instance, regardless of the VPC and network settings.

Determine the SSH port allocated.

```bash
ps auxw | grep $INSTANCE_ID
```

```bash
qemu-system-x86_64 -daemonize -pidfile /run/user/1000/i-36765eb0c6609e4d.pid -qmp unix:/run/user/1000/qmp-i-36765eb0c6609e4d2.sock,server,nowait -enable-kvm -M ubuntu -serial pty -cpu host -smp 2 -m 1024 -drive file=nbd://127.0.0.1:42653,format=raw,if=none,media=disk,id=os -drive file=nbd://127.0.0.1:44499,format=raw,if=virtio,media=cdrom,id=cloudinit -device virtio-blk-pci,drive=os,bootindex=1 -device virtio-rng-pci -device virtio-net-pci,netdev=net0 -netdev user,id=net0,hostfwd=tcp:127.0.0.1:33683-:22
```

Note the `hostfwd=tcp:127.0.0.1:33683-:22`, in this case the local port `33683` will connect to the new instance.

```bash
ssh -i ~/.ssh/hive-key ec2-user@127.0.0.1 -p 33683

...

Linux hive-vm-36765eb0 6.1.0-40-amd64 #1 SMP PREEMPT_DYNAMIC Debian 6.1.153-1 (2025-09-20) x86_64
ec2-user@hive-vm-36765eb0:~$
```

Congratulations! Your first AMI image is imported, a new EC2 instance launched, and successfully connected via SSH for the configured SSH key, using the OS `cloud-init` procedure.

## Managing Instances

### Stop Instance

To stop a running instance gracefully:

```bash
aws ec2 stop-instances --instance-ids $INSTANCE_ID
```

```json
{
  "StoppingInstances": [
    {
      "InstanceId": "i-36765eb0c6609e4d2",
      "CurrentState": {
        "Code": 64,
        "Name": "stopping"
      },
      "PreviousState": {
        "Code": 16,
        "Name": "running"
      }
    }
  ]
}
```

Next, confirm the instance has stopped as requested.

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

```json
{
  "Reservations": [
    {
      "ReservationId": "r-f101157331e261a68",
      "OwnerId": "123456789012",
      "Instances": [
        {
          "InstanceId": "i-36765eb0c6609e4d2",
          "ImageId": "ami-15c3dcfe607460f15",
          "State": {
            "Code": 80,
            "Name": "stopped"
          },
          "KeyName": "hive-key",
          "InstanceType": "t3.micro",
          "LaunchTime": "2025-11-12T13:07:47.548000+00:00"
        }
      ]
    }
  ]
}
```

### Start Instance

To start a previously stopped instance:

```bash
aws ec2 start-instances --instance-ids $INSTANCE_ID
```

```json
{
  "StartingInstances": [
    {
      "InstanceId": "i-36765eb0c6609e4d2",
      "CurrentState": {
        "Code": 0,
        "Name": "pending"
      },
      "PreviousState": {
        "Code": 80,
        "Name": "stopped"
      }
    }
  ]
}
```

Next, validate the instance is running as expected.

```bash
aws ec2 describe-instances  --instance-ids "i-36765eb0c6609e4d2"
```

```json
{
  "Reservations": [
    {
      "ReservationId": "r-f101157331e261a68",
      "OwnerId": "123456789012",
      "Instances": [
        {
          "InstanceId": "i-36765eb0c6609e4d2",
          "ImageId": "ami-15c3dcfe607460f15",
          "State": {
            "Code": 16,
            "Name": "running"
          },
          "KeyName": "hive-key",
          "InstanceType": "t3.micro",
          "LaunchTime": "2025-11-12T13:07:47.548000+00:00"
        }
      ]
    }
  ]
}
```

### Terminate Instance

To terminate an instance, which will first stop the instance, and on success, remove the EBS volumes and permanently remove the instance data.

```bash
aws ec2 terminate-instances --instance-ids $INSTANCE_ID
```

```json
{
  "TerminatingInstances": [
    {
      "InstanceId": "i-36765eb0c6609e4d2",
      "CurrentState": {
        "Code": 16,
        "Name": "running"
      },
      "PreviousState": {
        "Code": 16,
        "Name": "running"
      }
    }
  ]
}
```

Next, validate the instance is removed, this may take a few minutes depending on the instance volume size.

```bash
aws ec2 describe-instances  --instance-ids $INSTANCE_ID
```

On success no data will be returned, since the instance is no longer available.

## UI Management Panel

The Hive Platform also offers a web interface for managing hive services. Simply have the hive server running and go to [https://localhost:3000](https://localhost:3000) in your browser to continue.

**Note:** If you are not viewing the website on the same machine that is running the hive server, you will need to go to [https://localhost:9999](https://localhost:9999) and [https://localhost:8443](https://localhost:8443) and accept the certificates. If you have added the CA to your local machine this is not needed.

# Multi-Node Installation

Hive is designed for distributed, multi-server deployments. A Hive cluster operates as a fully distributed infrastructure region — similar to an AWS Region — where multiple nodes work together to provide high availability, data durability, and fault tolerance across the platform.

When deploying a multi-node cluster, you define a **region** (e.g., `ap-southeast-2`, `us-east-1`) and **availability zones** to organize your infrastructure. Services are distributed across nodes: NATS provides a clustered message bus for request routing and JetStream replication, Predastore uses Raft consensus with Reed-Solomon erasure coding (RS 2+1) for durable S3-compatible object storage, and the Hive daemon on each node can independently serve AWS API requests. The result is a platform where compute, storage, and API services are replicated — an AMI stored on one node is available from any node, an EC2 instance launched via any gateway is visible cluster-wide, and the loss of a single node does not compromise data or availability.

Cluster formation is automatic. When initializing with `--nodes 3`, the init node starts a formation server and waits for other nodes to join. Once all nodes have registered, each node receives the full cluster topology — credentials, CA certificates, NATS routes, Predastore peer lists — and generates its own configuration files. No manual configuration synchronization is required.

## Choose a Deployment Mode

| | Simulated (Option A) | Real Multi-Server (Option B) |
|---|---|---|
| **Servers** | 1 machine, loopback IPs | 3+ physical/virtual servers |
| **Use case** | Development & testing | Production & staging |
| **Network** | `127.0.0.1/2/3` (no setup) | Real LAN IPs |
| **Data dirs** | `~/node1/`, `~/node2/`, `~/node3/` | `~/hive/` on each server |

Both options use `HIVE_NODE1`, `HIVE_NODE2`, and `HIVE_NODE3` environment variables. Set them in the option you choose, then all subsequent sections (verification, usage, shutdown) reference them.

### Option A: Simulated Multi-Node (Single Server)

For development and testing, a 3-node cluster can be simulated on a single machine using loopback addresses. Linux handles the full `127.0.0.0/8` range natively, so `127.0.0.1`, `127.0.0.2`, and `127.0.0.3` all work without any network configuration. Each node uses a separate data directory to isolate configuration, logs, and state.

#### 1. Build and Set Variables

```bash
make build

export HIVE_NODE1=127.0.0.1
export HIVE_NODE2=127.0.0.2
export HIVE_NODE3=127.0.0.3
```

#### 2. Setup OVN

For simulated mode on a single machine, run the setup script once with `--management`:

```bash
./scripts/setup-ovn.sh --management
```

This starts OVN central services and configures the local compute node. All three simulated nodes share the same OVS/OVN instance on the host.

#### 3. Form the Cluster

The formation process requires running init and join commands concurrently. The init node blocks until all expected nodes have joined.

**Terminal 1 — Initialize the leader node:**

```bash
./bin/hive admin init \
  --node node1 \
  --nodes 3 \
  --bind $HIVE_NODE1 \
  --cluster-bind $HIVE_NODE1 \
  --port 4432 \
  --hive-dir ~/node1/ \
  --config-dir ~/node1/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a
```

This starts a formation server on `$HIVE_NODE1:4432` and waits for 2 more nodes to join.

**Terminal 2 — Join node 2 (while init is running):**

```bash
./bin/hive admin join \
  --node node2 \
  --bind $HIVE_NODE2 \
  --cluster-bind $HIVE_NODE2 \
  --host $HIVE_NODE1:4432 \
  --data-dir ~/node2/ \
  --config-dir ~/node2/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a
```

**Terminal 3 — Join node 3 (while init is running):**

```bash
./bin/hive admin join \
  --node node3 \
  --bind $HIVE_NODE3 \
  --cluster-bind $HIVE_NODE3 \
  --host $HIVE_NODE1:4432 \
  --data-dir ~/node3/ \
  --config-dir ~/node3/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a
```

All three processes will exit with a cluster summary once formation is complete.

#### 4. Start Services

Start services on each node (in separate terminals or background):

```bash
./scripts/start-dev.sh ~/node1/
./scripts/start-dev.sh ~/node2/
./scripts/start-dev.sh ~/node3/
```

### Option B: Real Multi-Node (Physical Servers)

For production or production-like deployments across multiple physical servers or VMs.

#### Prerequisites

On **each server**:

1. Install Hive dependencies (see [Dependencies](#dependencies) above)
2. Clone the repository and build:

```bash
mkdir -p ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
cd hive
./scripts/clone-deps.sh
make build
```

3. Identify the bind IP for each server:

```bash
ip addr show | grep "inet "
    inet 127.0.0.1/8 scope host lo
    inet 192.168.1.10/24 brd 192.168.1.255 scope global eth0
    inet 10.0.0.10/24 brd 10.0.0.255 scope global eth1
```

With a **single NIC**, use the one real IP (e.g. `192.168.1.10`) for everything. With **two NICs**, the primary interface (e.g. `eth0`) is your management IP for `--bind` and `--cluster-bind`, and the secondary (e.g. `eth1`) is for `--encap-ip` (Geneve overlay) — see [Dual NIC Configuration](#dual-nic-configuration).

#### 1. Set Variables

Export the IPs for your three servers (replace with your actual IPs) on each server:

```bash
export HIVE_NODE1=192.168.1.10
export HIVE_NODE2=192.168.1.11
export HIVE_NODE3=192.168.1.12
```

#### 2. Setup OVN

OVN must be configured on every server before forming the cluster. Server 1 runs OVN central (the NB and SB databases), so it must be set up first. Servers 2 and 3 connect to server 1's OVN databases as compute nodes.

**Note:** The examples below use a single NIC where `--encap-ip` is the same as the node's bind IP. If your servers have a dedicated overlay NIC, see [Dual NIC Configuration](#dual-nic-configuration) for how to separate management and Geneve tunnel traffic.

**Server 1 — OVN central + compute (run first):**

```bash
cd ~/Development/mulga/hive
./scripts/setup-ovn.sh --management --encap-ip=$HIVE_NODE1
```

Verify OVN central is ready before proceeding to other servers:

```bash
sudo ovn-sbctl show
```

**Server 2 — Compute node (after server 1 is ready):**

```bash
cd ~/Development/mulga/hive
./scripts/setup-ovn.sh --ovn-remote=tcp:$HIVE_NODE1:6642 --encap-ip=$HIVE_NODE2
```

**Server 3 — Compute node (after server 1 is ready):**

```bash
cd ~/Development/mulga/hive
./scripts/setup-ovn.sh --ovn-remote=tcp:$HIVE_NODE1:6642 --encap-ip=$HIVE_NODE3
```

Verify OVN is running on all servers:

```bash
sudo systemctl is-active ovn-controller
```

On server 1, confirm all chassis have registered:

```bash
sudo ovn-sbctl show
```

You should see 3 chassis entries, one per server, each with a Geneve encap IP.

If your servers have a dedicated data/overlay NIC separate from the management NIC, use the data NIC's IP for `--encap-ip` instead — see [Dual NIC Configuration](#dual-nic-configuration) below.

#### 3. Form the Cluster

**Server 1 — Initialize the cluster:**

```bash
cd ~/Development/mulga/hive

./bin/hive admin init \
  --node node1 \
  --nodes 3 \
  --bind $HIVE_NODE1 \
  --cluster-bind $HIVE_NODE1 \
  --port 4432 \
  --hive-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

**Server 2 — Join the cluster (while init is running):**

```bash
cd ~/Development/mulga/hive

./bin/hive admin join \
  --node node2 \
  --bind $HIVE_NODE2 \
  --cluster-bind $HIVE_NODE2 \
  --host $HIVE_NODE1:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

**Server 3 — Join the cluster (while init is running):**

```bash
cd ~/Development/mulga/hive

./bin/hive admin join \
  --node node3 \
  --bind $HIVE_NODE3 \
  --cluster-bind $HIVE_NODE3 \
  --host $HIVE_NODE1:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

All three processes will exit with a cluster summary once formation is complete.

#### 4. Start Services

After formation completes, start services on **all servers**:

```bash
./scripts/start-dev.sh
```

## Verify the Cluster

From any node, view cluster status and resource capacity:

```bash
./bin/hive get nodes
# NAME    STATUS    IP              REGION    AZ    UPTIME   VMs   SERVICES
# node1   Ready     $HIVE_NODE1     ...       ...   2m       0     nats,predastore,viperblock,daemon,...
# node2   Ready     $HIVE_NODE2     ...       ...   2m       0     nats,predastore,viperblock,daemon
# node3   Ready     $HIVE_NODE3     ...       ...   2m       0     nats,predastore,viperblock,daemon

./bin/hive top nodes
# NAME    CPU (used/total)   MEM (used/total)   VMs
# node1   0/16               0.0Gi/30.6Gi       0
# node2   0/16               0.0Gi/30.6Gi       0
# node3   0/16               0.0Gi/30.6Gi       0
```

All nodes should show `Ready` status. Use `./bin/hive get vms` to see running instances across the cluster.

Check individual daemon health endpoints:

```bash
curl -s http://$HIVE_NODE1:4432/health
curl -s http://$HIVE_NODE2:4432/health
curl -s http://$HIVE_NODE3:4432/health
```

Check NATS cluster routing (from any node):

```bash
grep -i "route\|cluster" ~/node1/logs/nats.log   # simulated
grep -i "route\|cluster" ~/hive/logs/nats.log     # real multi-server
```

Check Predastore Raft consensus:

```bash
grep -i "leader\|election" ~/node1/logs/predastore.log   # simulated
grep -i "leader\|election" ~/hive/logs/predastore.log     # real multi-server
```

## Trust CA Certificate

The CA certificate is generated by the init node and distributed to all joining nodes during formation. On **each node** (or the single machine for simulated mode), trust the CA:

```bash
sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates
```

For simulated mode, the config directory is under each node's data directory — copy from any node since the CA is the same:

```bash
sudo cp ~/node1/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates
```

Set the AWS profile:

```bash
export AWS_PROFILE=hive
```

## Using the Cluster

Connect to any node's AWS Gateway — all nodes serve the same cluster state:

```bash
AWS_ENDPOINT_URL=https://$HIVE_NODE1:9999 aws ec2 describe-instances
AWS_ENDPOINT_URL=https://$HIVE_NODE2:9999 aws ec2 describe-instances
```

### Create SSH Key

Create or import an SSH key pair from **any node** — the key is stored in Predastore and available cluster-wide:

```bash
aws ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

Or generate a new key pair:

```bash
aws ec2 create-key-pair \
  --key-name hive-key \
| jq -r '.KeyMaterial | rtrimstr("\n")' > ~/.ssh/hive-key
chmod 600 ~/.ssh/hive-key
```

**Important:** If you want to ssh into instances, you will need to copy the private key to every node in the cluster. Instance SSH port forwarding is only accessible from the node running the VM, so you need the key available on all nodes:

```bash
# From the node where the key was created, copy to other nodes:
scp ~/.ssh/hive-key tf-user@$HIVE_NODE2:~/.ssh/hive-key
scp ~/.ssh/hive-key tf-user@$HIVE_NODE3:~/.ssh/hive-key
```

### Create AMI Template

Import an OS image from **any node**. The AMI is stored in Predastore and available cluster-wide:

```bash
./bin/hive admin images list
./bin/hive admin images import --name debian-12-x86_64
```

Verify and export the AMI ID:

```bash
aws ec2 describe-images
export HIVE_AMI="ami-XXX"
```

See the [single-node AMI section](#create-ami-template) for more options (manual import, custom images).

### VPC Networking

Create a VPC and subnet for your cluster. This only needs to be done once — the VPC is shared across all nodes.

```bash
aws ec2 create-vpc --cidr-block 10.200.0.0/16
export HIVE_VPC="vpc-XXX"

aws ec2 create-subnet --vpc-id $HIVE_VPC --cidr-block 10.200.1.0/24
export HIVE_SUBNET="subnet-XXX"
```

Verify the OVN topology was created (from server 1):

```bash
sudo ovn-nbctl lr-list    # One logical router per VPC
sudo ovn-nbctl ls-list    # One logical switch per subnet
```

### Launching Instances Across Nodes

Use `--count` to launch multiple instances in a single request. Hive distributes them across nodes in the cluster based on available capacity — instances land on different physical hosts but share the same VPC subnet, and can route traffic to each other over OVN's Geneve overlay:

```bash
aws ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --subnet-id $HIVE_SUBNET \
  --count 3
```

This launches 3 instances distributed across the cluster. Each receives a unique private IP from the subnet (e.g. `10.200.1.5`, `10.200.1.6`, `10.200.1.7`). Instances on different physical hosts communicate transparently through Geneve tunnels — no additional configuration required.

Verify all instances are running and have received their private IPs:

```bash
aws ec2 describe-instances
```

Check which node each instance landed on:

```bash
./bin/hive get vms
```

### Verifying Cross-Host Connectivity

SSH into an instance. The dev SSH port forwarding binds to the node running the VM, so you must SSH from that node (or use its IP). Find the forwarded port:

```bash
# On the node running the instance:
ps auxw | grep hostfwd
# Look for: hostfwd=tcp:<node-ip>:<port>-:22
```

```bash
ssh -i ~/.ssh/hive-key ec2-user@<node-ip> -p <port>
```

From inside the VM, ping an instance running on a **different physical node**:

```bash
ping 10.200.1.6
```

If the ping succeeds across hosts, the OVN Geneve overlay is working correctly — traffic is being encapsulated and routed between physical servers transparently.

Check logs on each node for debugging. Log locations depend on your deployment mode — `~/node{1,2,3}/logs/` for simulated, `~/hive/logs/` for real multi-server.

## Advanced Configuration

### Service Co-location

Every node that runs EC2 instances (compute) **must** also run the viperblock service. EBS volumes are mounted locally via Unix domain sockets (NBD) — the daemon publishes to a node-specific NATS topic (`ebs.{nodeName}.mount`) which is handled by the viperblock instance on the same server. Nodes without viperblock cannot run EC2 instances.

In the default deployment, every node runs all services (NATS, Predastore, Viperblock, Daemon, Gateway), which satisfies this requirement.

### Network Configuration

Minimum **1 NIC** required. **2 NICs recommended** for production:

| NIC | Purpose | Traffic |
|-----|---------|---------|
| **NIC 1 — Management** | Cluster coordination, admin access | NATS cluster routes, Predastore Raft, daemon health, SSH, AWS Gateway |
| **NIC 2 — Overlay** | VPC networking between hosts | Geneve tunnels (UDP 6081), OVN datapath, inter-host VM traffic |

With a single NIC, all traffic shares one interface. This works but is not recommended for production — Geneve tunnel traffic competes with cluster coordination traffic.

### Dual NIC Configuration

When using two NICs, each flag controls which NIC carries which traffic:

| Flag | NIC | Used by | Example |
|------|-----|---------|---------|
| `--bind` | Management | Formation server, daemon health, AWS gateway | `192.168.1.10` |
| `--cluster-bind` | Management | NATS cluster routes, Predastore Raft consensus | `192.168.1.10` |
| `--encap-ip` | Overlay | Geneve tunnels (VM-to-VM traffic across hosts) | `10.0.0.10` |

If `--cluster-bind` is not specified, it defaults to `--bind`. For single NIC deployments, all three use the same IP.

Example with management NIC `192.168.1.0/24` and overlay NIC `10.0.0.0/24`. The order is the same as the single NIC flow — setup OVN on all servers first, then form the cluster.

**Step 1 — Setup OVN on all servers:**

```bash
# Server 1 (OVN central + compute):
./scripts/setup-ovn.sh --management --encap-ip=10.0.0.10

# Server 2 (compute, after server 1 is ready):
./scripts/setup-ovn.sh --ovn-remote=tcp:192.168.1.10:6642 --encap-ip=10.0.0.11

# Server 3 (compute, after server 1 is ready):
./scripts/setup-ovn.sh --ovn-remote=tcp:192.168.1.10:6642 --encap-ip=10.0.0.12
```

**Step 2 — Form the cluster (same as single NIC, but with `--cluster-bind`):**

Server 1:

```bash
./bin/hive admin init \
  --node node1 \
  --nodes 3 \
  --bind 192.168.1.10 \
  --cluster-bind 192.168.1.10 \
  --port 4432 \
  --hive-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

Server 2 (while init is running):

```bash
./bin/hive admin join \
  --node node2 \
  --bind 192.168.1.11 \
  --cluster-bind 192.168.1.11 \
  --host 192.168.1.10:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

Server 3 (while init is running):

```bash
./bin/hive admin join \
  --node node3 \
  --bind 192.168.1.12 \
  --cluster-bind 192.168.1.12 \
  --host 192.168.1.10:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region us-east-1 \
  --az us-east-1a
```

### IP Aliases (Simulated Mode)

For a more production-like simulation using dedicated IP addresses on a network interface:

```bash
sudo ip addr add 10.11.12.1/24 dev eth0
sudo ip addr add 10.11.12.2/24 dev eth0
sudo ip addr add 10.11.12.3/24 dev eth0
```

Replace `eth0` with your network interface name (e.g., `enp0s3`, `ens33`). Then export these as your `HIVE_NODE` variables instead of the loopback addresses.

To remove the aliases:

```bash
sudo ip addr del 10.11.12.1/24 dev eth0
sudo ip addr del 10.11.12.2/24 dev eth0
sudo ip addr del 10.11.12.3/24 dev eth0
```

## Shutdown

When running in the multi-server cluster mode a graceful shutdown is required to synchronize and coordinate nodes.

On any node, issue the following command to shutdown nodes cluster wide:

```sh
./bin/hive admin cluster shutdown
```

Example output:
```bash
Starting coordinated cluster shutdown (3 nodes)
Phases: gate -> drain -> storage -> persist -> infra
Timeout per phase: 2m0s

[GATE] Sending to 3 node(s)...
  [GATE] node1: stopped awsgw, hive-ui
  [GATE] node3: stopped awsgw, hive-ui
  [GATE] node2: stopped awsgw, hive-ui
[GATE] Complete (3/3 nodes, 2.005s)

[DRAIN] Sending to 3 node(s)...
  [DRAIN] node1: 0/0 VMs remaining
  [DRAIN] node1: 0/0 VMs remaining
  [DRAIN] node2: 0/0 VMs remaining
  [DRAIN] node2: 0/0 VMs remaining
  [DRAIN] node3: 0/0 VMs remaining
  [DRAIN] node3: 0/0 VMs remaining
  [DRAIN] node1: OK
  [DRAIN] node3: OK
  [DRAIN] node2: OK
[DRAIN] Complete (3/3 nodes, 5ms)

[STORAGE] Sending to 3 node(s)...
  [STORAGE] node1: stopped viperblock
  [STORAGE] node3: stopped viperblock
  [STORAGE] node2: stopped viperblock
[STORAGE] Complete (3/3 nodes, 1.09s)

[PERSIST] Sending to 3 node(s)...
  [PERSIST] node1: stopped predastore
  [PERSIST] node3: stopped predastore
  [PERSIST] node2: stopped predastore
[PERSIST] Complete (3/3 nodes, 1.003s)

[INFRA] Sending final shutdown to all nodes...
[INFRA] Complete
Cluster shutdown complete (6.104s)

```

If EC2 instances are running, the stop process will gracefully terminate them, unmount attached EBS volumes (via NBD), and flush the write-ahead-log (WAL) to the S3 server (Predastore). This may take a few minutes depending on instance volume sizes.

To fully remove a simulated cluster's data:

```bash
rm -rf ~/node1/ ~/node2/ ~/node3/
```
