#!/bin/bash

# Multi-node E2E test helper functions
# Provides utilities for managing simulated IPs, starting/stopping node services,
# and verifying NATS cluster health.

# Network configuration
SIMULATED_NETWORK="10.11.12"
NODE1_IP="${SIMULATED_NETWORK}.1"
NODE2_IP="${SIMULATED_NETWORK}.2"
NODE3_IP="${SIMULATED_NETWORK}.3"

# Port configuration
NATS_CLIENT_PORT=4222
NATS_CLUSTER_PORT=4248
NATS_MONITOR_PORT=8222
PREDASTORE_PORT=8443
AWSGW_PORT=9999
CLUSTER_PORT=4432

# Add simulated IPs to loopback interface
# Requires NET_ADMIN capability
add_simulated_ips() {
    echo "Adding simulated IPs to loopback interface..."

    for i in 1 2 3; do
        local ip="${SIMULATED_NETWORK}.$i"
        if ! ip addr show lo | grep -q "$ip"; then
            ip addr add "${ip}/24" dev lo
            echo "  Added $ip to lo"
        else
            echo "  $ip already exists on lo"
        fi
    done

    # Verify IPs were added
    echo "Verifying simulated IPs..."
    for i in 1 2 3; do
        local ip="${SIMULATED_NETWORK}.$i"
        if ip addr show lo | grep -q "$ip"; then
            echo "  $ip is configured"
        else
            echo "  ERROR: Failed to add $ip"
            return 1
        fi
    done

    echo "Simulated IPs configured successfully"
}

# Remove simulated IPs from loopback interface
remove_simulated_ips() {
    echo "Removing simulated IPs from loopback interface..."

    for i in 1 2 3; do
        local ip="${SIMULATED_NETWORK}.$i"
        if ip addr show lo | grep -q "$ip"; then
            ip addr del "${ip}/24" dev lo 2>/dev/null || true
            echo "  Removed $ip from lo"
        fi
    done

    echo "Simulated IPs removed"
}

# Start services for a specific node
# Usage: start_node_services <node_num> <data_dir>
# Example: start_node_services 1 ~/node1
start_node_services() {
    local node_num="$1"
    local data_dir="$2"
    local node_ip="${SIMULATED_NETWORK}.$node_num"

    echo "Starting services for node$node_num at $node_ip..."

    # Set environment variables for this node
    export CONFIG_DIR="$data_dir/config"

    # Start services using start-dev.sh with the node's data directory
    HIVE_SKIP_BUILD=true ./scripts/start-dev.sh "$data_dir"

    echo "Node$node_num services started"
}

# Stop services for a specific node
# Usage: stop_node_services <node_num> <data_dir>
stop_node_services() {
    local node_num="$1"
    local data_dir="$2"

    echo "Stopping services for node$node_num..."

    # Stop using PID files in the node's log directory
    ./scripts/stop-dev.sh "$data_dir"

    echo "Node$node_num services stopped"
}

# Stop all node services
stop_all_nodes() {
    echo "Stopping all node services..."

    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        if [ -d "$data_dir/logs" ]; then
            stop_node_services "$i" "$data_dir" || true
        fi
    done

    echo "All node services stopped"
}

# Verify NATS cluster health
# Checks that the cluster has expected number of members
# Usage: verify_nats_cluster [expected_members]
verify_nats_cluster() {
    local expected_members="${1:-3}"

    echo "Verifying NATS cluster health (expecting $expected_members members)..."

    # Check cluster info via monitoring endpoint on node1
    local cluster_info
    cluster_info=$(curl -s "http://${NODE1_IP}:${NATS_MONITOR_PORT}/routez" 2>/dev/null) || {
        echo "  ERROR: Cannot reach NATS monitoring endpoint"
        return 1
    }

    # Count routes (should be expected_members - 1 for a healthy cluster)
    local num_routes
    num_routes=$(echo "$cluster_info" | jq -r '.num_routes // 0')
    local expected_routes=$((expected_members - 1))

    echo "  NATS cluster routes: $num_routes (expected: $expected_routes)"

    if [ "$num_routes" -eq "$expected_routes" ]; then
        echo "  NATS cluster is healthy"
        return 0
    else
        echo "  WARNING: NATS cluster may not be fully formed"
        echo "  Cluster info: $cluster_info"
        return 1
    fi
}

# Wait for a specific instance state
# Usage: wait_for_instance_state <instance_id> <target_state> [max_attempts]
wait_for_instance_state() {
    local instance_id="$1"
    local target_state="$2"
    local max_attempts="${3:-30}"
    local attempt=0

    echo "Waiting for instance $instance_id to reach state: $target_state..."

    while [ $attempt -lt $max_attempts ]; do
        local state
        state=$(aws --endpoint-url https://localhost:${AWSGW_PORT} ec2 describe-instances \
            --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text 2>/dev/null) || {
            echo "  Attempt $((attempt + 1))/$max_attempts - Gateway request failed, retrying..."
            sleep 5
            attempt=$((attempt + 1))
            continue
        }

        echo "  Instance state: $state"

        if [ "$state" == "$target_state" ]; then
            echo "  Instance reached target state: $target_state"
            return 0
        fi

        if [ "$state" == "terminated" ] && [ "$target_state" != "terminated" ]; then
            echo "  ERROR: Instance terminated unexpectedly"
            return 1
        fi

        sleep 5
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Instance did not reach $target_state within $max_attempts attempts"
    return 1
}

# Wait for gateway to be ready
# Usage: wait_for_gateway [host] [max_attempts]
wait_for_gateway() {
    local host="${1:-localhost}"
    local max_attempts="${2:-30}"
    local attempt=0

    echo "Waiting for AWS Gateway at $host:${AWSGW_PORT}..."

    while [ $attempt -lt $max_attempts ]; do
        if curl -k -s "https://${host}:${AWSGW_PORT}" > /dev/null 2>&1; then
            echo "  Gateway is ready"
            return 0
        fi

        echo "  Waiting for gateway... ($((attempt + 1))/$max_attempts)"
        sleep 2
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Gateway failed to start"
    return 1
}

# Check instance distribution across nodes
# Returns the instance counts per node
check_instance_distribution() {
    echo "Checking instance distribution across nodes..."

    local total=0
    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local instances_file="$data_dir/hive/instances.json"

        if [ -f "$instances_file" ]; then
            local count
            count=$(jq 'length' "$instances_file" 2>/dev/null || echo "0")
            echo "  Node$i: $count instances"
            total=$((total + count))
        else
            echo "  Node$i: 0 instances (no instances file)"
        fi
    done

    echo "  Total instances: $total"
}

# Dump logs from all nodes (for debugging failures)
dump_all_node_logs() {
    echo ""
    echo "=========================================="
    echo "DUMPING LOGS FROM ALL NODES"
    echo "=========================================="

    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local logs_dir="$data_dir/logs"

        if [ -d "$logs_dir" ]; then
            echo ""
            echo "=== Node$i Logs ==="

            for log in nats predastore viperblock hive awsgw; do
                if [ -f "$logs_dir/$log.log" ]; then
                    echo ""
                    echo "--- $log.log (last 50 lines) ---"
                    tail -50 "$logs_dir/$log.log" 2>/dev/null || echo "(empty or not accessible)"
                fi
            done
        fi
    done

    echo ""
    echo "=========================================="
    echo "END OF LOG DUMP"
    echo "=========================================="
}

# Initialize leader node (node1)
# Usage: init_leader_node
init_leader_node() {
    echo "Initializing leader node (node1)..."

    ./bin/hive admin init \
        --node node1 \
        --bind "${NODE1_IP}" \
        --cluster-bind "${NODE1_IP}" \
        --region ap-southeast-2 \
        --az ap-southeast-2a \
        --nodes 3 \
        --hive-dir "$HOME/node1/" \
        --config-dir "$HOME/node1/config/"

    echo "Leader node initialized"
}

# Join a follower node to the cluster
# Usage: join_follower_node <node_num>
join_follower_node() {
    local node_num="$1"
    local node_ip="${SIMULATED_NETWORK}.$node_num"
    local data_dir="$HOME/node$node_num"

    echo "Joining node$node_num ($node_ip) to cluster..."

    # Build cluster routes (all other nodes)
    local routes="${NODE1_IP}:${NATS_CLUSTER_PORT}"

    ./bin/hive admin join \
        --node "node$node_num" \
        --bind "$node_ip" \
        --cluster-bind "$node_ip" \
        --cluster-routes "$routes" \
        --host "${NODE1_IP}:${CLUSTER_PORT}" \
        --data-dir "$data_dir/" \
        --config-dir "$data_dir/config/" \
        --region ap-southeast-2 \
        --az "ap-southeast-2a"

    echo "Node$node_num joined cluster"
}
