#!/bin/sh

# Linux
#genisoimage -output seed.iso -volid cidata -joliet -rock user-data meta-data

# MacOS
mkisofs -output seed.iso -volid cidata -joliet -rock meta-data user-data
