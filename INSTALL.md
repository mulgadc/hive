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

## Init

When running Hive for the first time, run the init function to create the default directories for data, config files and layout required.

```
./bin/hive admin init
```

Next, set the AWS profile to use `hive` which points to the local environment.

```
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

```
aws ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

### Create new key pair

Alternatively, create a new key pair and store the JSON output of the AWS SDK using the `jq` command.

```bash
aws ec2 create-key-pair \
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

```
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

Note the output to launch an instance, specifically the `"ImageId": ami-XXX` attribute.

```
export HIVE_AMI='ami-XXX'
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

## Run Instance

Once Hive is successfully installed and bootstrapped with a system AMI and SSH keys, proceed to run an instance. Replace `ami-XXX` with your imported ImageId above.

```
aws ec2 run-instances \
  --image-id $HIVE_AMI \
  --instance-type t3.micro \
  --key-name hive-key \
  --security-group-ids sg-0123456789abcdef0 \
  --subnet-id subnet-6e7f829e \
  --count 1
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
nats req --reply-timeout=5s ec2.cmd.i-1bb0cfd0e48bfa232 '{ "id": "i-1bb0cfd0e48bfa232", "command": { "execute": "system_reset", "arguments": {} } }'
```

### Query status

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "query-status", "arguments": {} } }'
```

### Query devices

```
nats req --reply-timeout=5s ec2.cmd.i-ebaf0fd46cad14c85 '{ "id": "i-ebaf0fd46cad14c85", "command": { "execute": "query-device", "arguments": {} } }'
```
