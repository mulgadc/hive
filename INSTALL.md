# Hive installation

## Dependencies

For running on Ubuntu 22.04

```bash
add-apt-repository universe
apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system  libvirt-clients libvirt-dev
```

Create hive user

```
adduser --disabled-password hive
```

Add hive to the libvirt group to manage VMs

```
adduser hive libvirt
```

## Build

```
make
```

## Services

Start libvirt networking (TODO, requires individual VPC support)

```bash
virsh -c qemu:///system net-start default
```

## Launch

```bash
nbdkit -p 10812 --pidfile /tmp/vb-vol-1.pid ../viperblock/lib/nbdkit-viperblock-plugin.so -v -f size=67108864 volume=vol-2 bucket=predastore region=ap-southeast-2 access_key="X" secret_key="Y" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=0
```

## Create AMI template

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
go run cmd/hive/main.go service predastore --base-path ~/hive/predastore/ --config-path ~/Development/mulga/hive/config/predastore/predastore.toml --tls-cert ~/Development/mulga/hive/config/server.pem --tls-key ~/Development/mulga/hive/config/server.key start
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
go run cmd/hive/main.go service nats start --debug
```

### Hive daemon

The background Hive daemon is a core service which accepts requests to provision services such as the AWS SDK/CLI.

```
go run cmd/hive/main.go --config config/hive.toml daemon
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
go run cmd/hive/main.go service nats start
```

### Predastore

```
go run cmd/hive/main.go service predastore --base-path ~/hive/predastore/ --config-path ~/Development/mulga/hive/config/predastore/predastore.toml --tls-cert ~/Development/mulga/hive/config/server.pem --tls-key ~/Development/mulga/hive/config/server.key  --debug start
```

### Viperblock

```
go run cmd/hive/main.go service viperblock start --nats-host 0.0.0.0:4222 --access-key "AKIAIOSFODNN7EXAMPLE" --secret-key "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" --base-dir /mnt/ramdisk/ --plugin-path ~/Development/mulga/viperblock/lib/nbdkit-viperblock-plugin.so
```

### Hive (Control Plane)

```
go run cmd/hive/main.go --config config/hive.toml --base-dir /mnt/ramdisk/ daemon
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
