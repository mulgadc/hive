#!/usr/bin/env bash
set -euo pipefail

# Configure a provisioned Hive cluster: clone repo, build, form cluster, start services.
# Usage: configure-cluster.sh <cluster_name> <state_file>

CLUSTER_NAME="${1:?Usage: configure-cluster.sh <cluster_name> <state_file>}"
STATE_FILE="${2:?Usage: configure-cluster.sh <cluster_name> <state_file>}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOFU_DIR="$SCRIPT_DIR/proxmox"

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10"

REGION="ap-southeast-2"
AZ="ap-southeast-2a"

inventory=$(tofu -chdir="$TOFU_DIR" output -json -state="$STATE_FILE" inventory)
NODE_COUNT=$(echo "$inventory" | jq -r '.node_count')
SSH_KEY=$(eval echo "$(echo "$inventory" | jq -r '.ssh_key_path')")
SSH_USER=$(echo "$inventory" | jq -r '.ssh_user')

# Build arrays of node IPs
declare -a MGMT_IPS
for i in $(seq 0 $((NODE_COUNT - 1))); do
    MGMT_IPS+=("$(echo "$inventory" | jq -r ".nodes[$i].management")")
done

remote() {
    local ip="$1"
    shift
    ssh -i "$SSH_KEY" $SSH_OPTS "$SSH_USER@$ip" "$@"
}

remote_bg() {
    local ip="$1"
    shift
    ssh -i "$SSH_KEY" $SSH_OPTS "$SSH_USER@$ip" "$@" &
}

echo "==> Cluster '$CLUSTER_NAME': $NODE_COUNT nodes"
for i in $(seq 0 $((NODE_COUNT - 1))); do
    echo "    node$((i + 1)): ${MGMT_IPS[$i]}"
done

# --- Step 1: Wait for cloud-init ---
echo ""
echo "==> Waiting for cloud-init to complete on all nodes..."
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${MGMT_IPS[$i]}"
    printf "    node%d (%s): " "$((i + 1))" "$ip"
    attempts=0
    while ! remote "$ip" "test -f /tmp/vendor-cloud-init-done" 2>/dev/null; do
        attempts=$((attempts + 1))
        if [ $attempts -ge 60 ]; then
            echo "TIMEOUT"
            echo "Error: cloud-init did not complete on $ip after 5 minutes"
            exit 1
        fi
        sleep 5
    done
    echo "done"
done

# --- Step 2: Clone repo and build on all nodes ---
echo ""
echo "==> Cloning hive repo and building on all nodes..."
PIDS=()
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${MGMT_IPS[$i]}"
    (
        echo "    node$((i + 1)) ($ip): cloning..."
        remote "$ip" "mkdir -p ~/Development/mulga && cd ~/Development/mulga && git clone https://github.com/mulgadc/hive.git && export PATH=\$PATH:/usr/local/go/bin && sudo make -C hive quickinstall"
        echo "    node$((i + 1)) ($ip): clone-deps + dev-setup..."
        remote "$ip" "export PATH=\$PATH:/usr/local/go/bin && cd ~/Development/mulga/hive && ./scripts/clone-deps.sh && ./scripts/dev-setup.sh"
        echo "    node$((i + 1)) ($ip): build complete"
    ) &
    PIDS+=($!)
done

for pid in "${PIDS[@]}"; do
    wait "$pid"
done
echo "    All nodes built successfully"

# --- Step 3: Form cluster ---
echo ""
echo "==> Forming cluster..."

# Build comma-separated IP lists
ALL_MGMT_IPS=$(IFS=,; echo "${MGMT_IPS[*]}")
NODE1_IP="${MGMT_IPS[0]}"

# Node 1: init
echo "    Starting init on node1 ($NODE1_IP)..."
remote_bg "$NODE1_IP" "export PATH=\$PATH:/usr/local/go/bin && cd ~/Development/mulga/hive && ./bin/hive admin init \
    --node node1 \
    --bind $NODE1_IP \
    --cluster-bind $NODE1_IP \
    --cluster-routes $NODE1_IP:4248 \
    --predastore-nodes $ALL_MGMT_IPS \
    --port 4432 \
    --nodes $NODE_COUNT \
    --region $REGION \
    --az $AZ \
    --hive-dir ~/hive/ \
    --config-dir ~/hive/config/"
INIT_PID=$!

# Wait for formation server to be ready
echo "    Waiting for formation server on $NODE1_IP..."
attempts=0
while ! curl -s --connect-timeout 3 "http://$NODE1_IP:4432/formation/health" >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ $attempts -ge 60 ]; then
        echo "Error: Formation server did not start on $NODE1_IP after 5 minutes"
        kill $INIT_PID 2>/dev/null || true
        exit 1
    fi
    sleep 5
done
echo "    Formation server ready"

# Nodes 2..N: join
if [ "$NODE_COUNT" -gt 1 ]; then
    JOIN_PIDS=()
    for i in $(seq 1 $((NODE_COUNT - 1))); do
        ip="${MGMT_IPS[$i]}"
        node_num=$((i + 1))
        echo "    Starting join on node$node_num ($ip)..."
        remote_bg "$ip" "export PATH=\$PATH:/usr/local/go/bin && cd ~/Development/mulga/hive && ./bin/hive admin join \
            --node node$node_num \
            --bind $ip \
            --cluster-bind $ip \
            --cluster-routes $NODE1_IP:4248 \
            --host $NODE1_IP:4432 \
            --data-dir ~/hive/ \
            --config-dir ~/hive/config/ \
            --region $REGION \
            --az $AZ"
        JOIN_PIDS+=($!)
    done

    for pid in "${JOIN_PIDS[@]}"; do
        wait "$pid"
    done
fi

# Wait for init process to complete
wait $INIT_PID
echo "    Cluster formation complete"

# --- Step 4: Start services ---
echo ""
echo "==> Starting services on all nodes..."
PIDS=()
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${MGMT_IPS[$i]}"
    (
        echo "    node$((i + 1)) ($ip): starting services..."
        remote "$ip" "export PATH=\$PATH:/usr/local/go/bin && cd ~/Development/mulga/hive && HIVE_SKIP_BUILD=true ./scripts/start-dev.sh"
        echo "    node$((i + 1)) ($ip): services started"
    ) &
    PIDS+=($!)
done

for pid in "${PIDS[@]}"; do
    wait "$pid"
done

# --- Step 5: Health check ---
echo ""
echo "==> Waiting for all nodes to become healthy..."
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${MGMT_IPS[$i]}"
    printf "    node%d (%s): " "$((i + 1))" "$ip"
    attempts=0
    while true; do
        health=$(curl -s --connect-timeout 3 "http://$ip:4432/health" 2>/dev/null || echo "")
        if [ -n "$health" ]; then
            echo "healthy"
            break
        fi
        attempts=$((attempts + 1))
        if [ $attempts -ge 30 ]; then
            echo "TIMEOUT"
            echo "Warning: node$((i + 1)) did not become healthy after 150 seconds"
            break
        fi
        sleep 5
    done
done

echo ""
echo "==> Cluster '$CLUSTER_NAME' configured successfully"
