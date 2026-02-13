#!/bin/bash

# Stop simulated multi-node cluster.
# Stops services in reverse order (node3 → node2 → node1).
# Usage: ./scripts/stop-multi-node.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Stopping multi-node cluster..."

# Stop in reverse order — node1 last since it hosts the Predastore leader
"$SCRIPT_DIR/stop-dev.sh" ~/node3/
"$SCRIPT_DIR/stop-dev.sh" ~/node2/
"$SCRIPT_DIR/stop-dev.sh" ~/node1/

echo ""
echo "Multi-node cluster stopped."
echo "To remove all data: rm -rf ~/node1/ ~/node2/ ~/node3/"
