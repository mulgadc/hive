
#sudo ip addr del 10.11.12.1/24 dev eth0
#sudo ip addr del 10.11.12.2/24 dev eth0
#sudo ip addr del 10.11.12.3/24 dev eth0

# TODO: Reverse order, predastore running on node1
# BUG: Need to wait for each QEMU to flush/commit WAL to disk > S3, before shutdown, else corruption may occur
# BUG: nbdkit: ramdisk.3: debug: sending error reply: Cannot send after transport endpoint shutdown
# TODO: Fallback, if WAL on local disk not flushed, on boot, viperblock should commit and verify. Requires UT and test-cases

./scripts/stop-dev.sh ~/node3/

./scripts/stop-dev.sh ~/node2/

./scripts/stop-dev.sh ~/node1/


