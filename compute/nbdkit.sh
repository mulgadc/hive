#!/bin/sh

# Example to launch nbdkit

./server/nbdkit -p 10810 --pidfile /tmp/vb-vol-1-efi.pid ./plugins/golang/examples/ramdisk/nbdkit-goramdisk-plugin.so -v -f size=67108864 volume=vol-50c968e8dbcc74bd2-efi bucket=predastore region=ap-southeast-2 access_key="AKIAIOSFODNN7EXAMPLE" secret_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" base_dir="/tmp/vb/" host="https://127.0.0.1:8443" cache_size=1
