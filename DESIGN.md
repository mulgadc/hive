# Design ideas

### hivectl

Basic commands to setup a compute instance, block storage (viperblock), object storage (predastore)

`hivectl configure --endpoint-url https://localhost:8443/`

`hivectl auth --access_key X --secret_key Y --profile hivelocal --endpoint-url https://localhost:8443/`

Writes to ~/.aws/credentials

`hivectl vol add --size 128M --no-verify-ssl --endpoint-url https://localhost:8443/ --`

Authenticates using ~/.aws/credentials - Backend creates a unique volume id

Connects to S3, creates the /vol-uniquestr/, uploads `config.json`

```
{
  "VolumeName": "vol-uniquestr",
  "VolumeSize": 134217728,
  "BlockSize": 4096,
  "ObjBlockSize": 4194304,
  "SeqNum": 0,
  "ObjectNum": 0,
  "WALNum": 0,
  "BlockToObjectWALNum": 0,
  "Version": 1
}
```

`hivectl ec2 import-key-pair --key-name my-key-pair --public-key-material fileb://~/.ssh/id_rsa.pub`

This will upload the users SSH public key to their S3 bucket, /bucket/ec2/my-key-pair.pub

`hivectl ec2 run-instances --image-id ami-X --instance-type t3.micro --key-name my-key-pair`

This will clone an existing image-id (vol-ID, e.g debian snapshot), copy to the users S3 bucket, and start the new volume from the fresh copy.

Note when starting an image additional volumes are required.

```
:~/qemu-host$ truncate -s 64m varstore.img
:~/qemu-host$ truncate -s 64m efi.img
:~/qemu-host$ dd if=/usr/share/qemu-efi-aarch64/QEMU_EFI.fd of=efi.img conv=notrunc
```

Sample launch

Require to start NBD (EFI)

./server/nbdkit -p 10810 --pidfile /tmp/vb-vol-1-efi.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=67108864 volume=vol-1-efi bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb" host="https://127.0.0.1:8443"

Require to start Debian volume

./server/nbdkit -p 10809 --pidfile /tmp/vb-vol-1-root.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=4294967296 volume=vol-1-root bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb" host="https://127.0.0.1:8443"

```
:~/qemu-host$ ~/qemu/build/aarch64-softmmu/qemu-system-aarch64 -M virt  \
      -machine virtualization=true -machine virt,gic-version=3  \
      -cpu max,pauth-impdef=on -smp 2 -m 4096           \
      -drive if=pflash,format=raw,file=efi.img,readonly=on      \
      -drive if=pflash,format=raw,file=varstore.img         \
      -drive if=virtio,format=qcow2,file=disk.img           \
      -device virtio-scsi-pci,id=scsi0              \
      -object rng-random,filename=/dev/urandom,id=rng0      \
      -device virtio-rng-pci,rng=rng0               \
      -device virtio-net-pci,netdev=net0                \
      -netdev user,id=net0,hostfwd=tcp::8022-:22            \
      -nographic                            \
      -drive if=none,id=cd,file=debian-12.2.0-arm64-netinst.iso \
      -device scsi-cd,drive=cd
```
