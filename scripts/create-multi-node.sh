# simulate node1
echo "Multi-node simulation environment"
make

echo "Adding simulated IPs: 10.11.12.1, 10.11.12.2, 10.11.12.3"

sudo ip addr add 10.11.12.1/24 dev eth0

# simulate node2
sudo ip addr add 10.11.12.2/24 dev eth0

# simulate node3
sudo ip addr add 10.11.12.3/24 dev eth0

echo "Removing old node directories..."

rm -rf ~/node1/
rm -rf ~/node2/
rm -rf ~/node3/

echo "Node1 Setup:"

# Initialize node1
./bin/hive admin init \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node1 \
--bind 10.11.12.1 \
--cluster-bind 10.11.12.1 \
--port 4432 \
--hive-dir ~/node1/ \
--config-dir ~/node1/config/

# Start node1
./scripts/start-dev.sh ~/node1/

echo "Node2 Setup:"

# Join cluster
./bin/hive admin join \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node2 \
--bind 10.11.12.2 \
--cluster-bind 10.11.12.2 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node2/ \
--config-dir ~/node2/config/ \

# Start node2
./scripts/start-dev.sh ~/node2/

echo "Node3 Setup:"

./bin/hive admin join \
--region ap-southeast-2 \
--az ap-southeast-2a \
--node node3 \
--bind 10.11.12.3 \
--cluster-bind 10.11.12.3 \
--cluster-routes 10.11.12.1:4248 \
--host 10.11.12.1:4432 \
--data-dir ~/node3/ \
--config-dir ~/node3/config/

./scripts/start-dev.sh ~/node3/