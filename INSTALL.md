# Hive Installation

Notes for development environment installation.

## Dependencies

For running on Ubuntu 22.04, 25.04 and 25.10.

```bash
sudo add-apt-repository universe
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system libvirt-clients libvirt-dev make gcc
```

Ensure the Go toolkit is installed, recommended to install the latest directly from [https://go.dev/dl/](https://go.dev/dl/).

Confirm go is correctly installed, and set in your $PATH.

```bash
go version
```

Hive provides AWS API/SDK layer functionality, which requires the AWS CLI tool to be installed to interface with the system.

```bash
sudo apt install awscli
```

## Build

Create the base directory for the Hive development environment.

```bash
mkdir -p ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
```

Setup the dev environment and package dependencies on [viperblock](https://github.com/mulgadc/viperblock/) and [predastore](https://github.com/mulgadc/predastore/).

```bash
cd hive
./scripts/clone-deps.sh    # Clone viperblock + predastore repositories
./scripts/dev-setup.sh     # Setup complete development environment
```

Once complete, confirm `./bin/hive` exists and executable.

# Single Node Installation

For rapid development and testing, `hive` can be installed locally as a single node instance. Follow the instructions below for a complete working environment.

## Init

When running Hive for the first time, run the init function to create the default directories for data, config files and layout required.

Example single node installation to get started, this will create a new region `ap-southeast-2` and availability zone `ap-southeast-2a` on your local instance:

```bash
./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
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

Update permissions for the key for ssh to accept reading the file.

```bash
chmod 600 ~/.ssh/hive-key
```

Next, generate a public key from the specified private key pair.

```bash
ssh-keygen -y -f ~/.ssh/hive-key > ~/.ssh/hive-key.pub
```

### Validate New Key

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

Use the hive CLI tool to import a selected OS image. Note, the source file can be compressed (e.g image.tar.gz, image.gz, image.tar.xz) and the tool will automatically extract and upload the raw OS image as an AMI after validation the disk image contains a UEFI/BIOS boot capability.

Note, when downloading OS images, use supported platforms that support the `cloud-init` feature to automatically bootstrap when using the Hive EC2 functionality to access SSH and networking services.

### Automatic Image Import

Discover available images to automatically download and install. This will pull the images from the distro official mirror and simplify the process to bootstrap a Hive installation with AMIs that include common operating systems.

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
./bin/hive admin images import --name debian-12-arm64 --force
```

Make note of the Image-ID `ami-XXX`

```bash
✅ Image import complete. Image-ID (AMI): ami-e29fcc65734aec9ea
```

Next, verify available disk images to confirm the import was successful, replace `ami-XXX` with the value from the command output.

```bash
aws ec2 describe-images --image-ids ami-XXX
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

Next, verify available disk images to confirm the import was successful.

```bash
aws ec2 describe-images
```

## Run Instance

Once Hive is successfully installed and bootstrapped with a system AMI and SSH keys, proceed to run an instance. Replace `ami-XXX` with your imported ImageId above.

```bash
export HIVE_AMI="ami-XXX"
```

Launch a new instance, note `hive-key` is the SSH key specified in the previous stage. If using an x86_64 host specify the `--instance-type t3.micro` otherwise if running on ARM64 use `--instance-type t4g.micro`

```bash
aws ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type t3.micro \
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

Export the instance ID for following the rest of the tutorial.

```bash
export INSTANCE_ID="i-XXX"
```

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

For a Hive development environment (toggled off for production), a local SSH port forwaring will be active to connect directly to the instance, regardless of the VPC and network settings.

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

Congratulations, your first AMI image is imported, a new EC2 instance launched, and successfully connected via SSH for the configured SSH key, using the OS `cloud-init` procedure.

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

To terminate an instance, which will first stop the instance, and on success, remove the EBS volumes and permanately remove the instance data.

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

Next, validate the instance is removed, this may take a few minutes depending the instance volume size.

```bash
aws ec2 describe-instances  --instance-ids $INSTANCE_ID
```

On success no data will be returned, since the instance is no longer available.

# Multi-node Configuration

To create a simulate multi-node installation on a single server (e.g node1, node2, node3) review the script `./scripts/create-multi-node.sh`.

Specifically this will create 3 IPs on a specified ethernet adapter.

```bash
ip addr add 10.11.12.1/24 dev eth0
ip addr add 10.11.12.2/24 dev eth0
ip addr add 10.11.12.3/24 dev eth0
```

Next, `hive` will be installed and initiated in `$HOME/node1` for configuration files.

```bash
# Initialize node1
./bin/hive admin init \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node1 \
--bind 10.11.12.1 \
--cluster-bind 10.11.12.1 \
--port 4432 \
--hive-dir ~/node1/ \
--config-dir ~/node1/config/
```

Two other nodes will be created, joining node1 to exchange configuration files and identify as a new node.

```bash
# Join cluster
./bin/hive admin join \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node2 \
--bind 10.11.12.2 \
--cluster-bind 10.11.12.2 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node2/ \
--config-dir ~/node2/config/ \

# Start node2
./scripts/start-dev.sh ~/node2/

echo "Node3 Setup:"

./bin/hive admin join \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node3 \
--bind 10.11.12.3 \
--cluster-bind 10.11.12.3 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node3/ \
--config-dir ~/node3/config/

./scripts/start-dev.sh ~/node3/
```

Once configured, each node will have it's own storage for config, data and state in `~/node[1,2,3]` to simulate a unique node and network.

Once the multi-node is configured, follow the original installation instructions above to:

- Import an SSH key
- Clone an AMI
- Launch an instance

Note - when using the AWS CLI tool you must specify the IP address of the node (10.11.12.1, 10.11.12.2, or 10.11.12.3 ) and ignore SSL verification for development purposes, specifically appending the arguments `--endpoint-url https://10.11.12.3:9999/ --no-verify-ssl`.

```bash
aws --endpoint-url https://10.11.12.3:9999/ --no-verify-ssl ec2 describe-instances --insta
nce-ids i-9f5f648adc57ea46d

urllib3/connectionpool.py:1064: InsecureRequestWarning: Unverified HTTPS request is being made to host '10.11.12.3'. Adding certificate verification is strongly advised. See: https://urllib3.readthedocs.io/en/1.26.x/advanced-usage.html#ssl-warnings
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

Note if multiple EC2 instances are running, it make take a few minutes to gracefully terminate the instance, unmount the attached EBS volume (via NBD) and push the write-ahead-log (WAL) to the S3 server (predastore).
