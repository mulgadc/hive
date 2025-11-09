# Hive installation

## Dependencies

For running on Ubuntu 22.04

```bash
sudo add-apt-repository universe
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system  libvirt-clients libvirt-dev
```

Create hive user

```
sudo adduser --disabled-password hive
```

Add hive to the libvirt group to manage VMs

```
sudo adduser hive libvirt
```

Hive provided AWS API/SDK layer functionality, which requires the AWS CLI tool to be installed to interface with the system.

```
sudo apt install awscli
```


## Build

Compile `hive` binary which will be used throughout the installation tutorial.

```
make
```

Confirm ./bin/hive exists and executable.

```
./bin/hive

__/\\\________/\\\__/\\\\\\\\\\\__/\\\________/\\\__/\\\\\\\\\\\\\\\_
 _\/\\\_______\/\\\_\/////\\\///__\/\\\_______\/\\\_\/\\\///////////__
  _\/\\\_______\/\\\_____\/\\\_____\//\\\______/\\\__\/\\\_____________
   _\/\\\\\\\\\\\\\\\_____\/\\\______\//\\\____/\\\___\/\\\\\\\\\\\_____
    _\/\\\/////////\\\_____\/\\\_______\//\\\__/\\\____\/\\\///////______
     _\/\\\_______\/\\\_____\/\\\________\//\\\/\\\_____\/\\\_____________
      _\/\\\_______\/\\\_____\/\\\_________\//\\\\\______\/\\\_____________
       _\/\\\_______\/\\\__/\\\\\\\\\\\______\//\\\_______\/\\\\\\\\\\\\\\\_
        _\///________\///__\///////////________\///________\///////////////__

Hive – Open source AWS-compatible platform for secure edge deployments.
Run EC2, VPC, S3, and EBS-like services on bare metal with full control.
Built for environments where running in the cloud isn’t an option.
Whether you’re deploying to edge sites, private data-centers, or operating
in low-connectivity or highly contested environments

Usage:
  hive [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  service     Manage Hive services

Flags:
      --access-key string     AWS access key (overrides config file and env)
      --base-dir string       Viperblock base directory (overrides config file and env)
      --config string         config file (required)
  -h, --help                  help for hive
      --host string           AWS Endpoint (overrides config file and env)
      --nats-host string      NATS server host (overrides config file and env)
      --nats-subject string   NATS subscription subject (overrides config file and env)
      --nats-token string     NATS authentication token (overrides config file and env)
      --secret-key string     AWS secret key (overrides config file and env)

Use "hive [command] --help" for more information about a command.
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

```
aws --debug --endpoint-url https://localhost:9999 --no-verify-ssl ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

### Create new key pair

Alternatively, create a new key pair and store the JSON output of the AWS SDK using the `jq` command.

```bash
aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 create-key-pair \
  --key-name hive-key \
| jq -r '.KeyMaterial | rtrimstr("\n")' > ~/.ssh/hive-key
```

Update permissions to SSH will accept reading the file.

```bash
chmod 600 ~/.ssh/hive-key
```

Next, generate a public key from the specified private key pair.

```bash
ssh-keygen -y -f ~/.ssh/hive-key > ~/.ssh/hive-key.pub
```

Validate the new key is available

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

```
Name                 | Distro | Version | Arch  
alpine-3.22.2-arm64  | alpine | 3.22.2  | arm64 
alpine-3.22.2-x86_64 | alpine | 3.22.2  | x86_64
debian-12-arm64      | debian | 12      | arm64 
debian-12-x86_64     | debian | 12      | x86_64
ubuntu-24.04-arm64   | ubuntu | 24.04   | arm64 
ubuntu-24.04-x86_64  | ubuntu | 24.04   | x86_64
```

Next, choose the image you would like to import as an AMI.

```bash
./bin/hive admin images import --name debian-12-arm64 --force
```

```
Downloading image https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-arm64.tar.xz to /home/ben/hive/images/debian/12/arm64/debian-12-generic-amd64.tar.xz
Downloading local-debian-12-arm64 [283748988/283748988] ██████████████ 100% | 1s
Saved /home/ben/hive/images/debian/12/arm64/debian-12-generic-amd64.tar.xz (270.6 MiB)
Extracted image to: /home/ben/hive/images/debian/12/arm64/disk.raw

AMI import complete
```

Next, verify available disk images to confirm the import was successful

```bash
aws ec2 describe-images
```

### Manual import

Using this method you can import any OS disk image. For example, download the Debian 12 image from the repository [https://cloud.debian.org/images/cloud/bookworm/latest/](https://cloud.debian.org/images/cloud/bookworm/latest/)

Download the image:

```
wget https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-arm64.tar.xz -O ~/debian-12-genericcloud-arm64.tar.xz
```

Import as an AMI to the backend store:

```
./bin/hive admin images import --file ~/debian-12-genericcloud-arm64.tar.xz --arch arm64 --distro debian --version 12
```

Next, verify available disk images to confirm the import was successful

```
aws ec2 describe-images
```

## Create AMI template (old method)

Manual import method for development, suggest using new method above.

### Step 1

Download the selected image

```bash
wget https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2 -O $HOME/debian-12-genericcloud-amd64.qcow2
```

Confirm nbd available in the kernel

```bash
lsmod | grep nbd
```

If missing enable in the kernel

```bash
modprobe nbd
```

Add nbd to be included at boot if missing.

```bash
echo nbd | sudo tee -a /etc/modules
```

Mount the qcow2 image

```bash
sudo qemu-nbd -r --connect=/dev/nbd0 $HOME/debian-12-genericcloud-amd64.qcow2
```

### Step 2

Confirm disk attributes

```bash
fdisk -l /dev/nbd0
```

Note a Linux cloud image should be similar to the following:

```bash
Disk /dev/nbd0: 3 GiB, 3221225472 bytes, 6291456 sectors
Units: sectors of 1 * 512 = 512 bytes
Sector size (logical/physical): 512 bytes / 512 bytes
I/O size (minimum/optimal): 512 bytes / 512 bytes
Disklabel type: gpt
Disk identifier: 7C03A441-F052-CB42-A148-27D2974B71B0

Device        Start     End Sectors  Size Type
/dev/nbd0p1  262144 6289407 6027264  2.9G Linux root (x86-64)
/dev/nbd0p14   2048    8191    6144    3M BIOS boot
/dev/nbd0p15   8192  262143  253952  124M EFI System
```

### Step 3

Copy block by block (zero blocks will be skipped), requires root to read the raw /dev/nbd0 device. Confirm the $SIZE attribute from the fdisk output above.

Export the following and replace with your ACCESS_KEY & SECRET_KEY defined in `hive/config/predastore/predastore.toml`.

```bash
export VSIZE=3221225472
export ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
export SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

Create a temporary ramdisk for performance when creating images, or substitute the location for a higher speed (e.g nvme drive)

```bash
mount -t tmpfs -o size=8G tmpfs /mnt/ramdisk/
```

## Step 4

Ensure predastore is currently running

```bash
./bin/hive service predastore --base-path ~/hive/predastore/ --config-path ~/Development/mulga/hive/config/predastore/predastore.toml --tls-cert ~/Development/mulga/hive/config/server.pem --tls-key ~/Development/mulga/hive/config/server.key start
```

## Step 5

Define the AMI attributes, see the sample template at `viperblock/tests/import-ami.json`, if using the example `debian-12-genericcloud-amd64.qcow2` the sample template can be used.

```json
{
    "VolumeMetadata": {
        "VolumeID": "",
        "VolumeName": "debian-12.10.0-ami",
        "TenantID": "tenant-001",
        "SizeGiB": 0,
        "State": "available",
        "AvailabilityZone": "ap-southeast-2",
        "AttachedInstance": "",
        "DeviceName": "",
        "VolumeType": "gp3",
        "IOPS": 1000,
        "Tags": null,
        "SnapshotID": "",
        "IsEncrypted": false
    },
    "AMIMetadata": {
        "ImageID": "",
        "Name": "debian-12.10.0",
        "Description": "Debian 12.10.0 minimal cloud image prepared for Hive",
        "Architecture": "arm64",
        "PlatformDetails": "Linux/UNIX",
        "RootDeviceType": "ebs",
        "Virtualization": "hvm",
        "ImageOwnerAlias": "hive",
        "VolumeSizeGiB": 0,
        "Tags": null
}
```

Import the raw disk as a new AMI

```bash
sudo ../viperblock/bin/vblock -file=/dev/nbd0 -size=$VSIZE -volume=ami-debian-12-genericcloud -bucket=predastore -region=ap-southeast-2 -access_key="$ACCESS_KEY" -secret_key="$SECRET_KEY" -base_dir="/mnt/ramdisk/vbimport" -host="https://127.0.0.1:8443" -metadata=../viperblock/tests/import-ami.json
```

At the end of the import the new AMI ID will be referenced, e.g

```json
time=2025-07-16T12:36:18.016+10:00 level=INFO msg="VB Close, flushing block state to disk"
time=2025-07-16T12:36:18.016+10:00 level=DEBUG msg="OpenWAL complete, new WAL" file={file:0xc00009c780}
time=2025-07-16T12:36:18.017+10:00 level=DEBUG msg="Saving Close state to" path=/mnt/ramdisk/vbimport/ami-185c47c7b6d31bba9
```

Unmount the temp filesystem

```bash
umount /mnt/ramdisk/
```

## Start services

### NATS

An embedded NATS server provides messaging between components of Hive and is a requirement.

```
./bin/hive service nats start --debug
```

### Hive daemon

The background Hive daemon is a core service which accepts requests to provision services such as the AWS SDK/CLI.

```
./bin/hive service hive start --config config/hive.toml
```

## Provision instance

Once the required services are running, use the examples provided below to provision services.

### EC2 instance

### SSH setup

The first step is to provide a public SSH key which will be used by `cloud-init` when a new EC2 (Linux) instance is launched.

Upload your public-key to the predastore S3 repository using the AWS CLI tool. Confirm your credentials are correctly defined in `~/.aws/credentials` that match the predastore configuration file `config/predastore/predastore.toml` and the region defined.

```
cat ~/.aws/credentials
[predastore]
aws_access_key_id = AKIAIOSFODNN7EXAMPLE
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

Confirm access is working and the previous AMI is visible:

```
AWS_REGION=ap-southeast-2 aws --no-verify-ssl --endpoint-url https://localhost:844
3/ s3 ls s3://predastore/
```

Output:

```
                           PRE ami-185c47c7b6d31bba9/
```

Upload SSH key

```
AWS_REGION=ap-southeast-2 aws --no-verify-ssl --endpoint-url https://localhost:8443/ s3 cp ~/.ssh/id_rsa.pub s3://predastore/ssh/test-keypair.pub
upload: ../../../.ssh/id_rsa.pub to s3://predastore/ssh/test-keypair.pub
```

### Launch instance

To launch a sample EC2 instance edit `tests/ec2.json` and replace the AMI ID from the previous step (e.g ami-185c47c7b6d31bba9)

tests/ec2.json:

```
{
    "Action": "RunInstances",
    "ImageId": "ami-185c47c7b6d31bba9",
    "InstanceType": "t3.micro",
    "KeyName": "test-keypair.pub",
    "SecurityGroups": [
        "MySecurityGroup"
    ],
    "SubnetId": "subnet-6e7f829e",
    "MaxCount": 1,
    "MinCount": 1,
    "Version": "2016-11-15"
}
```

Publish the EC2 request using the NATS cli tool.

```
cat tests/ec2.json | nats --trace pub ec2.launch
```

# Launching services

### NATS

```
./bin/hive service nats start
```

### Predastore

```
./bin/hive service predastore --base-path ~/hive/predastore/ --config-path ~/Development/mulga/hive/config/predastore/predastore.toml --tls-cert ~/Development/mulga/hive/config/server.pem --tls-key ~/Development/mulga/hive/config/server.key  --debug start
```

### Viperblock

```
./bin/hive service viperblock start --nats-host 0.0.0.0:4222 --access-key "AKIAIOSFODNN7EXAMPLE" --secret-key "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" --base-dir /mnt/ramdisk/ --plugin-path ~/Development/mulga/viperblock/lib/nbdkit-viperblock-plugin.so
```

### Hive (Control Plane)

```
./bin/hive service hive start --config config/hive.toml --base-dir /mnt/ramdisk/
```

# Example events

### Mount volume

```

```

### Unmount volume

### Launch EC2

```
nats req ec2.launch '{"Action":"RunInstances","ImageId":"ami-185c47c7b6d31bba9","InstanceType":"t3.micro","KeyName":"test-keypair.pub","SecurityGroups":["MySecurityGroup"],"SubnetId":"subnet-6e7f829e","MaxCount":1,"MinCount":1,"Version":"2016-11-15"}'
```

### Describe instance

```
nats req --reply-timeout=5s ec2.describe.i-ebaf0fd46cad14c85 '{ "InstanceID": "i-ebaf0fd46cad14c85" }'
```

### Start instance

```
nats req ec2.startinstances '{"InstanceID": "i-f38ac0490f1683650"}'
```


## QMP commands

### Powerdown

This event is used internally be Hive when the daemon receives a SIGINT, SIGTERM, SIGHUP signal to safely powerdown an instance, or when the hardware node is rebooted.

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "system_powerdown", "arguments": {} } }'
```

### Terminate instance

This event is for a user initiated instance termination. Note the Attributes, to flag to the Hive daemon not to start the instance again if the daemon or hardware node is restarted.

```
nats req --reply-timeout=5s ec2.cmd.i-f38ac0490f1683650 '{ "id": "i-f38ac0490f1683650", "attributes": { "stop_instance": true }, "command": { "execute": "system_powerdown", "arguments": {} } }'
```

### Stop VM

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "stop", "arguments": {} } }'
```

### Resume VM

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "cont", "arguments": {} } }'
```

### Restart

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "system_reset", "arguments": {} } }'
```

### Query status

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "query-status", "arguments": {} } }'
```

### Query devices

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "query-device", "arguments": {} } }'
```
