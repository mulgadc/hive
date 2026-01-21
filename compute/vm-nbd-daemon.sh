#!/bin/sh

qemu-system-aarch64 \
   -nographic \
   -M virt,highmem=off \
   -accel hvf \
   -cpu host \
   -smp 4 \
   -m 3000 \
   -drive file=/Users/benduncan/qemu/QEMU_EFI.img,if=pflash,format=raw \
   -device virtio-blk-pci,drive=debian,bootindex=1 \
   -drive if=none,media=disk,id=debian,format=raw,file=nbd://192.168.64.5:10809/default \
   -drive format=raw,file=nbd://192.168.64.5:10810/default,if=pflash \
   -drive file=nbd://192.168.64.5:10811/default,format=raw,if=virtio \
   -device qemu-xhci \
   -device usb-kbd \
   -device usb-tablet \
   -device intel-hda \
   -device hda-duplex \
   -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-device,netdev=net0 \
   -device virtio-rng-pci


# ./server/nbdkit -p 10809 --pidfile /tmp/vb-vol-1.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=4294967296 volume=vol-e660ff0065ad21f74 bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=20

# ./server/nbdkit -p 10810 --pidfile /tmp/vb-vol-1-efi.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=67108864 volume=vol-e660ff0065ad21f74-efi bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=1

# ./server/nbdkit -p 10811 --pidfile /tmp/vb-vol-1-cloudinit.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=1048576 volume=vol-e660ff0065ad21f74-cloudinit bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=1
