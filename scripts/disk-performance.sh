#!/bin/sh

# Performance scripts
sudo apt update
sudo apt install -y fio sysstat jq util-linux

# make sure not written to tmp disk, etc
mkdir $HOME/bench

fio --name=randrw_70_30 \
    --directory=$HOME/bench \
    --rw=randrw \
    --rwmixread=70 \
    --bs=4k \
    --size=128M \
    --numjobs=4 \
    --iodepth=32 \
    --ioengine=libaio \
    --direct=1 \
    --group_reporting

