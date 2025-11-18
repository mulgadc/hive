# Hive installation

Notes for development environment installation.

## Dependencies

For running on Ubuntu 22.04 and 25.04 (tested)

```bash
sudo add-apt-repository universe
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system  libvirt-clients libvirt-dev make gcc
```

Ensure the Go toolkit is installed, recommended to install the latest directly from [https://go.dev/dl/](https://go.dev/dl/)

Confirm go is correctly installed, and set in your $PATH

```
go version
```

```
go version go1.25.4 linux/amd64
```

Hive provided AWS API/SDK layer functionality, which requires the AWS CLI tool to be installed to interface with the system.

```bash
sudo apt install awscli
```

## Build

Create the base directory for the Hive development environment

```
mkdir ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
```

Setup the dev environment and package dependencies on [viperblock](https://github.com/mulgadc/viperblock/) and [predastore](https://github.com/mulgadc/predastore/)

```
# Setup dependencies and development environment
./scripts/clone-deps.sh    # Clone viperblock + predastore repositories
./scripts/dev-setup.sh     # Setup complete development environment
```

Confirm ./bin/hive exists and executable.

## Init

When running Hive for the first time, run the init function to create the default directories for data, config files and layout required.

Example single node installation to get started:

```
/bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

Next, set the AWS profile to use `hive` which points to the local environment.

```bash
export AWS_PROFILE=hive
```

## Launch services

Start the core services

```bash
./scripts/start-dev.sh
```

## Create SSH key

For first install users, create or import an existing key pair which can be used to launch EC2 instances.

### Import existing key

Import an existing key pair, replace `~/.ssh/id_rsa.pub` with your specified key.

```bash
aws ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

### Create new key pair

Alternatively, create a new key pair and store the JSON output of the AWS SDK using the `jq` command.

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

### Validate the new key is available

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

## Create AMI template (new method)

Use the hive CLI tool to import a selected OS image. Note, the source file can be compressed (e.g image.tar.gz, image.gz, image.tar.xz) and the tool will automatically extract and upload the raw OS image as an AMI.

Note, when downloading OS images, use supported platforms that support the `cloud-init` feature to automatically bootstrap when using the Hive EC2 functionality.

### Automatic import

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

Next, choose the image you would like to import as an AMI.

```bash
./bin/hive admin images import --name debian-12-arm64 --force
```

```bash
Downloading image https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-arm64.tar.xz to /home/ben/hive/images/debian/12/arm64/debian-12-generic-amd64.tar.xz
Downloading local-debian-12-arm64 [283748988/283748988] ██████████████ 100% | 1s
Saved /home/ben/hive/images/debian/12/arm64/debian-12-generic-amd64.tar.xz (270.6 MiB)
Extracted image to: /home/ben/hive/images/debian/12/arm64/disk.raw

✅ Image import complete. Image-ID (AMI): ami-e29fcc65734aec9ea
```

Next, verify available disk images to confirm the import was successful, replace `ami-XXX` with the value from the command output.

```bash
aws ec2 describe-images --image-ids ami-XXX
```

### Manual AMI import

Using this method you can import any OS disk image. For example, download the Debian 12 image from the repository [https://cloud.debian.org/images/cloud/bookworm/latest/](https://cloud.debian.org/images/cloud/bookworm/latest/)

Download the image:

```bash
wget https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-arm64.tar.xz -O ~/debian-12-genericcloud-arm64.tar.xz
```

Import as an AMI to the backend store:

```bash
./bin/hive admin images import --file ~/debian-12-genericcloud-arm64.tar.xz --arch arm64 --distro debian --version 12
```

Next, verify available disk images to confirm the import was successful

```bash
aws ec2 describe-images
```

## Run Instance

Once Hive is successfully installed and bootstrapped with a system AMI and SSH keys, proceed to run an instance. Replace `ami-XXX` with your imported ImageId above.

```bash
export HIVE_AMI="ami-XXX"
```

Launch a new instance, note `hive-key` is the SSH key specified in the previous stage.

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

Export the instance ID for following the rest of the tutorial

```bash
export INSTANCE_ID="i-XXX"
```

Next, validate the running instance is ready

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

Confirm the `State.Name` attribute is set as `running`

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

## SSH connection (development)

For a Hive development environment (toggled off for production), a local SSH port forwaring will be active to connect directly to the instance, regardless of the VPC and network settings.

Determine the SSH port allocated

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

## Managing instances

### Stop instance

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

Next, confirm the instance has stopped as requested

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

Next, validate the instance is running as expected

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

### Terminate instance

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

# Multi-node configuration

Details for setting up a multi-node configuration of Mulga for development use.
