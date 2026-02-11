# Hive Installation

Notes for development environment installation.

## Dependencies

The currently supported operating systems that are validated and tested include:

- Ubuntu 22.04
- Ubuntu 24.04
- Ubuntu 25.10

For the development preview, please use one of the supported versions above.

## Download

Create the base directory for the Hive development environment.

```bash
mkdir -p ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
```

### Quick Install

To bootstrap the dependencies of Hive in one simple step (QEMU, Go, AWS CLI):

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
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system libvirt-clients libvirt-dev make gcc unzip xz-utils file
```

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

For rapid development and testing, `hive` can be installed locally as a single node instance. Follow the instructions below for a complete working environment.

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
alpine-3.22.2-x86_64 | alpine | 3.22.2  | x86_64 | bios
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

## Run Instance

Once Hive is successfully installed and bootstrapped with a system AMI and SSH keys, proceed to run an instance.

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
      "InstanceType": "m8a.small",
      "CurrentGeneration": true,
      "SupportedRootDeviceTypes": ["ebs"],
      "SupportedVirtualizationTypes": ["hvm"],
      "Hypervisor": "kvm",
      "ProcessorInfo": {
        "SupportedArchitectures": ["x86_64"]
      },
      "VCpuInfo": {
        "DefaultVCpus": 2
      },
      "MemoryInfo": {
        "SizeInMiB": 1024
      },
      "BurstablePerformanceSupported": false
    }
  ]
}
```

Export the instance-type:

```sh
export HIVE_INSTANCE="m8a.small"
```

### Run Instance

Next, launch a new instance, note `hive-key` is the SSH key specified in the previous stage.

```bash
aws ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type $HIVE_INSTANCE \
  --key-name hive-key \
  --security-group-ids sg-0123456789abcdef0 \
  --subnet-id subnet-6e7f829e \
  --count 1
```

A sample response is below from the `RunInstance` request, note the `InstanceId` attribute:

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
      "KeyName": "hive-key",
      "InstanceType": "t3.micro",
      "LaunchTime": "2025-11-12T13:07:47.548000+00:00"
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

Confirm the `State.Name` attribute is set as `running`.

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

# Multi-node Configuration

To create a simulated multi-node installation on a single server (e.g node1, node2, node3) review the script `./scripts/create-multi-node.sh`.

Specifically this will create 3 IPs on a specified ethernet adapter.

```bash
ip addr add 10.11.12.1/24 dev eth0
ip addr add 10.11.12.2/24 dev eth0
ip addr add 10.11.12.3/24 dev eth0
```

### Initialize the Leader Node

Next, `hive` will be installed and initiated in `$HOME/node1` for configuration files.

```bash
./bin/hive admin init \
--node node1 \
--bind 10.11.12.1 \
--cluster-bind 10.11.12.1 \
--cluster-routes 10.11.12.1:4248 \
--predastore-nodes 10.11.12.1,10.11.12.2,10.11.12.3 \
--port 4432 \
--hive-dir ~/node1/ \
--config-dir ~/node1/config/ \
--region ap-southeast-2 \
--az ap-southeast-2a \

./scripts/start-dev.sh ~/node1/
```

### Join Follower Nodes

Two other nodes will be created, joining node1 to exchange configuration files and identify as a new node.

```bash
./bin/hive admin join \
--node node2 \
--bind 10.11.12.2 \
--cluster-bind 10.11.12.2 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node2/ \
--config-dir ~/node2/config/ \
--region ap-southeast-2 \
--az ap-southeast-2a \

./scripts/start-dev.sh ~/node2/
```

```bash
./bin/hive admin join \
--node node3 \
--bind 10.11.12.3 \
--cluster-bind 10.11.12.3 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node3/ \
--config-dir ~/node3/config/ \
--region ap-southeast-2 \
--az ap-southeast-2a \

./scripts/start-dev.sh ~/node3/
```

### Verify the Cluster

Each node will have its own storage for config, data and state in `~/node[1,2,3]` to simulate a unique node and network.

Once the multi-node cluster is configured, follow the original installation instructions above to:

- Import an SSH key
- Import an AMI
- Launch instances

Note - when using the AWS CLI tool you must specify the IP address of a gateway node (10.11.12.1, 10.11.12.2, or 10.11.12.3).

```bash
aws --endpoint-url https://10.11.12.3:9999 ec2 describe-instances --instance-ids i-9f5f648adc57ea46d
```

```json
{
  "Reservations": [
    {
      "ReservationId": "r-9d26866c3b0d53bf4",
      "OwnerId": "123456789012",
      "Instances": [
        {
          "InstanceId": "i-9f5f648adc57ea46d",
          "ImageId": "ami-d0de73dd18fa33ac9",
          "State": {
            "Code": 16,
            "Name": "running"
          },
          "KeyName": "hive-key",
          "InstanceType": "t3.micro",
          "LaunchTime": "2025-11-21T10:36:42.834000+00:00"
        }
      ]
    }
  ]
}
```

For debugging, reference the node unique configuration files, e.g

```bash
tail -n 100 ~/node3/logs/hive.log
```

```bash
...
2025/11/21 22:59:32 Received message on subject: ec2.DescribeInstances
2025/11/21 22:59:32 Message data: {"DryRun":null,"Filters":null,"InstanceIds":["i-9f5f648adc57ea46d"],"MaxResults":null,"NextToken":null}
2025/11/21 22:59:32 INFO Processing DescribeInstances request from this node
2025/11/21 22:59:32 INFO handleEC2DescribeInstances completed count=1
...
```

### Stop multi-node Instance

To stop a simulated multi-node instance:

```bash
./scripts/stop-multi-node.sh
```

Note if multiple EC2 instances are running, it may take a few minutes to gracefully terminate the instance, unmount the attached EBS volume (via NBD) and push the write-ahead-log (WAL) to the S3 server (predastore).
