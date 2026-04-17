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
./bin/spx admin init \
  --node node1 \
  --nodes 3 \
  --bind 127.0.0.1 \
  --cluster-bind 127.0.0.1 \
  --port 4432 \
  --spinifex-dir ~/node1/ \
  --config-dir ~/node1/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a &
INIT_PID=$!

# Wait for formation server to start
echo "Waiting for formation server..."
for i in $(seq 1 30); do
  if curl -sk "https://127.0.0.1:4432/formation/health" > /dev/null 2>&1; then
    echo "Formation server ready"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "ERROR: Formation server failed to start"
    kill $INIT_PID 2>/dev/null || true
    exit 1
  fi
  sleep 1
done

# Read join token
JOIN_TOKEN=$(cat ~/node1/config/join-token)
if [ -z "$JOIN_TOKEN" ]; then
  echo "ERROR: Join token file is empty"
  exit 1
fi

# Join nodes concurrently
./bin/spx admin join \
  --node node2 \
  --bind 127.0.0.2 \
  --cluster-bind 127.0.0.2 \
  --host 127.0.0.1:4432 \
  --token "$JOIN_TOKEN" \
  --data-dir ~/node2/ \
  --config-dir ~/node2/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a &
JOIN2_PID=$!

./bin/spx admin join \
  --node node3 \
  --bind 127.0.0.3 \
  --cluster-bind 127.0.0.3 \
  --host 127.0.0.1:4432 \
  --token "$JOIN_TOKEN" \
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
UI=false ./scripts/start-dev.sh ~/node1/ &
sleep 5

# Start nodes 2 and 3
UI=false ./scripts/start-dev.sh ~/node2/ &
sleep 2
UI=false ./scripts/start-dev.sh ~/node3/ &

# Wait for all start scripts to finish
wait

echo ""
echo "Step 3: Verification"
echo "===================="
sleep 5

echo "Daemon health:"
for IP in 127.0.0.1 127.0.0.2 127.0.0.3; do
  echo "  $IP: $(curl -sk https://$IP:4432/health | head -c 100)"
done

echo ""
echo "Multi-node cluster is running!"
echo "  Stop with: ./scripts/stop-dev.sh"
echo "  Logs: ~/node{1,2,3}/logs/"
