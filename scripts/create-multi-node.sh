#!/bin/bash

# Simulated multi-node cluster on a single machine using loopback addresses.
# Uses ~/node{1,2,3}/ for separate data directories.
# Usage: ./scripts/create-multi-node.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

echo "Multi-node simulation environment"
echo "================================="

# Build first
make build

echo ""
echo "Removing old node directories..."
rm -rf ~/node1/ ~/node2/ ~/node3/

echo ""
echo "Step 1: Cluster Formation"
echo "========================="
echo "Starting init node (127.0.0.1) and joining nodes (127.0.0.2, 127.0.0.3)..."

# Init blocks until all nodes join, so run in background
./bin/hive admin init \
  --node node1 \
  --nodes 3 \
  --bind 127.0.0.1 \
  --cluster-bind 127.0.0.1 \
  --port 4432 \
  --hive-dir ~/node1/ \
  --config-dir ~/node1/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a &
INIT_PID=$!

# Wait for formation server to start
sleep 2

# Join nodes concurrently
./bin/hive admin join \
  --node node2 \
  --bind 127.0.0.2 \
  --cluster-bind 127.0.0.2 \
  --host 127.0.0.1:4432 \
  --data-dir ~/node2/ \
  --config-dir ~/node2/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a &
JOIN2_PID=$!

./bin/hive admin join \
  --node node3 \
  --bind 127.0.0.3 \
  --cluster-bind 127.0.0.3 \
  --host 127.0.0.1:4432 \
  --data-dir ~/node3/ \
  --config-dir ~/node3/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a &
JOIN3_PID=$!

# Wait for all formation processes to complete
wait $INIT_PID $JOIN2_PID $JOIN3_PID
echo ""
echo "Cluster formation complete!"

echo ""
echo "Step 2: Starting Services"
echo "========================="

# Start node 1 first (Predastore needs quorum)
HIVE_SKIP_BUILD=true UI=false ./scripts/start-dev.sh ~/node1/ &
sleep 5

# Start nodes 2 and 3
HIVE_SKIP_BUILD=true UI=false ./scripts/start-dev.sh ~/node2/ &
sleep 2
HIVE_SKIP_BUILD=true UI=false ./scripts/start-dev.sh ~/node3/ &

# Wait for all start scripts to finish
wait

echo ""
echo "Step 3: Verification"
echo "===================="
sleep 5

echo "Daemon health:"
for IP in 127.0.0.1 127.0.0.2 127.0.0.3; do
  echo "  $IP: $(curl -s http://$IP:4432/health | head -c 100)"
done

echo ""
echo "Multi-node cluster is running!"
echo "  Stop with: ./scripts/stop-multi-node.sh"
echo "  Logs: ~/node{1,2,3}/logs/"
