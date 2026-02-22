#!/bin/bash

# Multi-node E2E test helper functions
# Provides utilities for managing simulated IPs, starting/stopping node services,
# and verifying NATS cluster health.


# Bootstrap OVN/OVS services inside a Docker container (no systemd).
# Starts ovsdb-server, ovs-vswitchd, OVN central (NB/SB DBs + northd),
# creates br-int, and starts ovn-controller.
# All 3 simulated nodes share one br-int and one ovn-controller.
bootstrap_ovn_docker() {
    echo "Bootstrapping OVN/OVS for Docker (no systemd)..."

    # Start ovsdb-server
    mkdir -p /var/run/openvswitch
    if [ ! -f /etc/openvswitch/conf.db ]; then
        ovsdb-tool create /etc/openvswitch/conf.db /usr/share/openvswitch/vswitch.ovsschema
    fi
    ovsdb-server --remote=punix:/var/run/openvswitch/db.sock \
        --remote=db:Open_vSwitch,Open_vSwitch,manager_options \
        --pidfile --detach --log-file=/var/log/openvswitch/ovsdb-server.log \
        /etc/openvswitch/conf.db
    ovs-vsctl --no-wait init

    # Start ovs-vswitchd
    ovs-vswitchd --pidfile --detach --log-file=/var/log/openvswitch/ovs-vswitchd.log

    # Create br-int with secure fail-mode
    ovs-vsctl --may-exist add-br br-int
    ovs-vsctl set Bridge br-int fail-mode=secure
    ovs-vsctl set Bridge br-int other-config:disable-in-band=true
    ip link set br-int up

    # Start OVN central (NB + SB DBs + northd)
    mkdir -p /var/run/ovn /var/lib/ovn /var/log/ovn

    # NB database
    if [ ! -f /var/lib/ovn/ovnnb_db.db ]; then
        ovsdb-tool create /var/lib/ovn/ovnnb_db.db /usr/share/ovn/ovn-nb.ovsschema
    fi
    ovsdb-server --remote=punix:/var/run/ovn/ovnnb_db.sock \
        --remote=ptcp:6641 \
        --pidfile=/var/run/ovn/ovnnb_db.pid \
        --detach --log-file=/var/log/ovn/ovnnb_db.log \
        /var/lib/ovn/ovnnb_db.db

    # SB database
    if [ ! -f /var/lib/ovn/ovnsb_db.db ]; then
        ovsdb-tool create /var/lib/ovn/ovnsb_db.db /usr/share/ovn/ovn-sb.ovsschema
    fi
    ovsdb-server --remote=punix:/var/run/ovn/ovnsb_db.sock \
        --remote=ptcp:6642 \
        --pidfile=/var/run/ovn/ovnsb_db.pid \
        --detach --log-file=/var/log/ovn/ovnsb_db.log \
        /var/lib/ovn/ovnsb_db.db

    # Start ovn-northd
    ovn-northd --pidfile=/var/run/ovn/ovn-northd.pid \
        --detach --log-file=/var/log/ovn/ovn-northd.log \
        --ovnnb-db=unix:/var/run/ovn/ovnnb_db.sock \
        --ovnsb-db=unix:/var/run/ovn/ovnsb_db.sock

    # Configure OVS external_ids for OVN
    local chassis_id="chassis-docker"
    ovs-vsctl set Open_vSwitch . \
        external_ids:system-id="$chassis_id" \
        external_ids:ovn-remote="tcp:127.0.0.1:6642" \
        external_ids:ovn-encap-ip="127.0.0.1" \
        external_ids:ovn-encap-type="geneve"

    # Start ovn-controller with pidfile in OVS rundir
    ovn-controller --pidfile=/var/run/openvswitch/ovn-controller.pid \
        --detach --log-file=/var/log/ovn/ovn-controller.log

    # Apply sysctl for overlay networking
    sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
    sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null 2>&1 || true
    sysctl -w net.ipv4.conf.default.rp_filter=0 >/dev/null 2>&1 || true

    # Load geneve kernel module
    modprobe geneve 2>/dev/null || true

    # Wait for ovn-controller to become responsive.
    # ovn-controller creates its ctl socket at /var/run/ovn/ovn-controller.PID.ctl
    # but ovs-appctl -t ovn-controller looks in /var/run/openvswitch/, so we
    # symlink it once the socket appears.
    echo "  Waiting for ovn-controller to start..."
    local ovn_pid
    ovn_pid=$(cat /var/run/openvswitch/ovn-controller.pid 2>/dev/null || true)
    local attempt=0
    while [ $attempt -lt 15 ]; do
        # Create symlink once the ctl socket appears
        if [ -n "$ovn_pid" ] && [ -S "/var/run/ovn/ovn-controller.${ovn_pid}.ctl" ] \
            && [ ! -e "/var/run/openvswitch/ovn-controller.${ovn_pid}.ctl" ]; then
            ln -sf "/var/run/ovn/ovn-controller.${ovn_pid}.ctl" \
                "/var/run/openvswitch/ovn-controller.${ovn_pid}.ctl"
        fi
        if ovs-appctl -t ovn-controller version >/dev/null 2>&1; then
            break
        fi
        sleep 1
        attempt=$((attempt + 1))
    done

    # Verify
    if ovs-vsctl br-exists br-int && ovs-appctl -t ovn-controller version >/dev/null 2>&1; then
        echo "  OVN/OVS bootstrap complete (br-int up, ovn-controller running)"
    else
        echo "  ERROR: OVN bootstrap failed"
        ovs-vsctl br-exists br-int && echo "  br-int: OK" || echo "  br-int: MISSING"
        ovs-appctl -t ovn-controller version >/dev/null 2>&1 && echo "  ovn-controller: OK" || echo "  ovn-controller: NOT RUNNING"
        # Dump ovn-controller log for debugging
        echo "  --- ovn-controller.log ---"
        cat /var/log/ovn/ovn-controller.log 2>/dev/null | tail -20 || true
        return 1
    fi
}

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

    # Start all services - each node's config binds to its specific IP
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

    # Count unique remote servers (NATS creates multiple connections per peer)
    local num_routes
    num_routes=$(echo "$cluster_info" | jq -r '.num_routes // 0')

    # Count unique peer names
    local unique_peers
    unique_peers=$(echo "$cluster_info" | jq -r '[.routes[].remote_name] | unique | length')
    local expected_peers=$((expected_members - 1))

    echo "  NATS cluster routes: $num_routes (unique peers: $unique_peers, expected peers: $expected_peers)"

    if [ "$unique_peers" -ge "$expected_peers" ]; then
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
    local max_attempts="${3:-10}"
    local attempt=0

    echo "Waiting for instance $instance_id to reach state: $target_state..."

    while [ $attempt -lt $max_attempts ]; do
        local state
        state=$(aws --endpoint-url https://${NODE1_IP}:${AWSGW_PORT} ec2 describe-instances \
            --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text) || {
            echo "  Attempt $((attempt + 1))/$max_attempts - Gateway request failed, retrying..."
            sleep 2
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

        sleep 2
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Instance did not reach $target_state within $max_attempts attempts"
    return 1
}

# Wait for gateway to be ready
# Usage: wait_for_gateway [host] [max_attempts]
wait_for_gateway() {
    local host="${1:-localhost}"
    local max_attempts="${2:-15}"
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

# Wait for daemon NATS subscriptions to be active.
# Polls describe-instance-types until it returns a non-empty result.
# Usage: wait_for_daemon_ready <gateway_endpoint> [max_attempts]
wait_for_daemon_ready() {
    local endpoint="$1"
    local max_attempts="${2:-15}"
    local attempt=0

    echo "Waiting for daemon readiness (NATS subscriptions)..."

    while [ $attempt -lt $max_attempts ]; do
        local types
        types=$(aws --endpoint-url "$endpoint" ec2 describe-instance-types \
            --query 'InstanceTypes[*].InstanceType' --output text 2>/dev/null || true)
        if [ -n "$types" ] && [ "$types" != "None" ]; then
            echo "  Daemon is ready"
            return 0
        fi
        echo "  Waiting... ($((attempt + 1))/$max_attempts)"
        sleep 2
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Daemon not ready after $max_attempts attempts"
    return 1
}

# Check instance distribution across nodes
# Counts QEMU processes per node IP from hostfwd bindings
check_instance_distribution() {
    echo "Checking instance distribution across nodes..."

    local total=0
    for i in 1 2 3; do
        local node_ip="${SIMULATED_NETWORK}.$i"
        # Count QEMU processes with hostfwd bound to this node's IP
        local count
        count=$(ps auxw | grep qemu-system | grep -v grep | grep -c "hostfwd=tcp:${node_ip}:" 2>/dev/null) || count=0
        echo "  Node$i ($node_ip): $count instances"
        total=$((total + count))
    done

    echo "  Total instances: $total"
}

# Find the SSH port for an instance from the QEMU process
# Usage: get_ssh_port <instance_id>
# Returns: the SSH port number, or empty string if not found
get_ssh_port() {
    local instance_id="$1"
    local max_attempts="${2:-30}"
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        local qemu_cmd
        qemu_cmd=$(ps auxw | grep "$instance_id" | grep qemu-system | grep -v grep || true)

        if [ -n "$qemu_cmd" ]; then
            # Extract port from hostfwd=tcp:127.0.0.1:PORT-:22 or hostfwd=tcp::PORT-:22
            local ssh_port
            ssh_port=$(echo "$qemu_cmd" | sed -n 's/.*hostfwd=tcp:[^:]*:\([0-9]*\)-:22.*/\1/p')

            if [ -n "$ssh_port" ]; then
                echo "$ssh_port"
                return 0
            fi
        fi

        attempt=$((attempt + 1))
        if [ $attempt -lt $max_attempts ]; then
            sleep 1
        fi
    done

    return 1
}

# Find the SSH host IP for an instance from the QEMU process hostfwd setting
# Usage: get_ssh_host <instance_id>
# Returns: the SSH host IP (e.g., 10.11.12.1, 127.0.0.1), or "127.0.0.1" if not found/empty
get_ssh_host() {
    local instance_id="$1"

    local qemu_cmd
    qemu_cmd=$(ps auxw | grep "$instance_id" | grep qemu-system | grep -v grep || true)

    if [ -n "$qemu_cmd" ]; then
        # Extract host from hostfwd=tcp:HOST:PORT-:22
        # Pattern matches: hostfwd=tcp:IP:PORT-:22 where IP can be empty
        local ssh_host
        ssh_host=$(echo "$qemu_cmd" | sed -n 's/.*hostfwd=tcp:\([^:]*\):[0-9]*-:22.*/\1/p')

        if [ -n "$ssh_host" ]; then
            echo "$ssh_host"
            return 0
        fi
    fi

    # Default to localhost if not found
    echo "127.0.0.1"
    return 0
}

# Find which node an instance is running on (for multi-node setups)
# Usage: get_instance_node <instance_id>
# Returns: node number (1, 2, or 3), or empty if not found
get_instance_node() {
    local instance_id="$1"

    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local instances_file="$data_dir/hive/instances.json"

        if [ -f "$instances_file" ]; then
            if jq -e ".[\"$instance_id\"]" "$instances_file" > /dev/null 2>&1; then
                echo "$i"
                return 0
            fi
        fi
    done

    return 1
}

# Wait for SSH to be ready on an instance
# Usage: wait_for_ssh <host> <port> <key_file> [max_attempts]
# Returns: 0 if SSH is ready, 1 if timeout
wait_for_ssh() {
    local host="$1"
    local port="$2"
    local key_file="$3"
    local max_attempts="${4:-60}"
    local attempt=0

    echo "  Waiting for SSH to be ready on $host:$port..."

    while [ $attempt -lt $max_attempts ]; do
        # Add host key to known_hosts (suppress errors)
        ssh-keyscan -p "$port" "$host" >> ~/.ssh/known_hosts 2>/dev/null || true

        # Try to connect with short timeout
        if ssh -o StrictHostKeyChecking=no \
               -o UserKnownHostsFile=/dev/null \
               -o ConnectTimeout=2 \
               -o BatchMode=yes \
               -p "$port" \
               -i "$key_file" \
               ec2-user@"$host" 'echo ready' > /dev/null 2>&1; then
            echo "  SSH is ready"
            return 0
        fi

        attempt=$((attempt + 1))
        if [ $attempt -lt $max_attempts ]; then
            if [ $((attempt % 10)) -eq 0 ]; then
                echo "  Waiting for SSH... ($attempt/$max_attempts)"
            fi
            sleep 1
        fi
    done

    echo "  ERROR: SSH not ready after $max_attempts attempts"
    return 1
}

# Test SSH connectivity by running 'id' command and verifying ec2-user in output
# Usage: test_ssh_connectivity <host> <port> <key_file>
# Returns: 0 if successful and ec2-user found, 1 otherwise
test_ssh_connectivity() {
    local host="$1"
    local port="$2"
    local key_file="$3"

    echo "  Testing SSH connectivity (running 'id' command)..."

    local id_output
    id_output=$(ssh -o StrictHostKeyChecking=no \
                    -o UserKnownHostsFile=/dev/null \
                    -o ConnectTimeout=5 \
                    -o BatchMode=yes \
                    -p "$port" \
                    -i "$key_file" \
                    ec2-user@"$host" 'id' 2>&1) || {
        echo "  ERROR: SSH command failed"
        echo "  Output: $id_output"
        return 1
    }

    echo "  SSH 'id' output: $id_output"

    if echo "$id_output" | grep -q "ec2-user"; then
        echo "  SSH connectivity test passed (ec2-user confirmed)"
        return 0
    else
        echo "  ERROR: Expected 'ec2-user' in id output"
        return 1
    fi
}

# Verify SSH is NOT reachable (used after instance termination)
# Usage: verify_ssh_unreachable <host> <port> <key_file>
# Returns: 0 if SSH is unreachable (expected), 1 if SSH is still reachable (unexpected)
verify_ssh_unreachable() {
    local host="$1"
    local port="$2"
    local key_file="$3"

    echo "  Verifying SSH is no longer reachable..."

    # Try to connect with 1 second timeout - should fail
    if ssh -o StrictHostKeyChecking=no \
           -o UserKnownHostsFile=/dev/null \
           -o ConnectTimeout=1 \
           -o BatchMode=yes \
           -p "$port" \
           -i "$key_file" \
           ec2-user@"$host" 'echo connected' > /dev/null 2>&1; then
        echo "  ERROR: SSH is still reachable after termination"
        return 1
    else
        echo "  SSH is no longer reachable (as expected)"
        return 0
    fi
}

# Expect an AWS CLI command to fail with a specific error code
# Usage: expect_error "ErrorCode" aws ec2 some-command --args...
# Returns: 0 if the command fails with the expected error, 1 otherwise
expect_error() {
    local expected_error="$1"
    shift

    set +e
    local output
    output=$("$@" 2>&1)
    local exit_code=$?
    set -e

    if [ $exit_code -eq 0 ]; then
        echo "  FAIL: Expected error '$expected_error' but command succeeded"
        echo "  Output: $output"
        return 1
    fi

    if echo "$output" | grep -q "$expected_error"; then
        echo "  Got expected error: $expected_error"
        return 0
    else
        echo "  FAIL: Expected error '$expected_error' but got different error"
        echo "  Output: $output"
        return 1
    fi
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

# Get the QEMU process PID for an instance
# Usage: get_qemu_pid <instance_id>
# Returns: the PID, or empty string if not found
get_qemu_pid() {
    local instance_id="$1"

    local pid
    pid=$(ps auxw | grep "$instance_id" | grep qemu-system | grep -v grep | awk '{print $2}' | head -1)

    if [ -n "$pid" ]; then
        echo "$pid"
        return 0
    fi

    return 1
}

# Wait for an instance to recover from a crash (error → running)
# Usage: wait_for_instance_recovery <instance_id> [max_attempts]
# Expects state transition: error → pending → running
wait_for_instance_recovery() {
    local instance_id="$1"
    local max_attempts="${2:-30}"
    local attempt=0
    local saw_error=false

    echo "  Waiting for instance $instance_id to recover..."

    while [ $attempt -lt $max_attempts ]; do
        local state
        state=$(aws --endpoint-url https://${NODE1_IP}:${AWSGW_PORT} ec2 describe-instances \
            --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text 2>/dev/null) || {
            sleep 2
            attempt=$((attempt + 1))
            continue
        }

        if [ "$state" == "error" ] || [ "$state" == "pending" ]; then
            saw_error=true
            echo "  State: $state (attempt $((attempt + 1))/$max_attempts)"
        fi

        if [ "$state" == "running" ] && [ "$saw_error" = true ]; then
            echo "  Instance recovered to running state"
            return 0
        fi

        if [ "$state" == "running" ] && [ "$saw_error" = false ]; then
            # Still in original running state, crash not detected yet
            :
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Instance did not recover within $max_attempts attempts"
    return 1
}

# Verify all services are down on all nodes
# Returns 0 if everything is down, 1 if something is still running
verify_all_services_down() {
    local all_down=true

    for i in 1 2 3; do
        local node_ip="${SIMULATED_NETWORK}.$i"

        # Check gateway
        if curl -k -s --connect-timeout 2 "https://${node_ip}:${AWSGW_PORT}" > /dev/null 2>&1; then
            echo "  Node$i: gateway still responding"
            all_down=false
        fi

        # Check NATS
        if curl -s --connect-timeout 2 "http://${node_ip}:${NATS_MONITOR_PORT}" > /dev/null 2>&1; then
            echo "  Node$i: NATS still responding"
            all_down=false
        fi
    done

    # Check for any remaining QEMU processes
    if pgrep -x qemu-system-x86_64 > /dev/null 2>&1; then
        echo "  QEMU processes still running"
        all_down=false
    fi

    if [ "$all_down" = true ]; then
        echo "  All services confirmed down"
        return 0
    fi
    return 1
}

# Force-kill all service processes and clean up stale resources on all nodes.
# Used between shutdown and restart to ensure a clean slate.
# This kills processes by PID file, then by name, removes badger LOCK files,
# and waits for ports to be free.
force_cleanup_all_nodes() {
    echo "Force-cleaning all nodes..."

    # Step 1: Kill all service processes via PID files
    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local logs_dir="$data_dir/logs"

        if [ -d "$logs_dir" ]; then
            for svc in hive-ui hive awsgw viperblock predastore nats; do
                local pidfile="$logs_dir/$svc.pid"
                if [ -f "$pidfile" ]; then
                    local pid
                    pid=$(cat "$pidfile" 2>/dev/null || true)
                    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
                        echo "  Node$i: killing $svc (PID $pid)..."
                        kill -TERM "$pid" 2>/dev/null || true
                    fi
                fi
            done
        fi
    done

    # Brief wait for graceful shutdown
    sleep 3

    # Step 2: SIGKILL anything still alive
    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local logs_dir="$data_dir/logs"

        if [ -d "$logs_dir" ]; then
            for svc in hive-ui hive awsgw viperblock predastore nats; do
                local pidfile="$logs_dir/$svc.pid"
                if [ -f "$pidfile" ]; then
                    local pid
                    pid=$(cat "$pidfile" 2>/dev/null || true)
                    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
                        echo "  Node$i: force-killing $svc (PID $pid)..."
                        kill -9 "$pid" 2>/dev/null || true
                    fi
                fi
            done
        fi
    done

    # Kill any remaining QEMU processes
    pkill -9 -x qemu-system-x86_64 2>/dev/null || true

    sleep 2

    # Step 3: Remove stale badger LOCK files from predastore directories
    for i in 1 2 3; do
        local data_dir="$HOME/node$i"
        local predastore_dir="$data_dir/predastore"

        if [ -d "$predastore_dir" ]; then
            local lock_files
            lock_files=$(find "$predastore_dir" -name "LOCK" -type f 2>/dev/null || true)
            if [ -n "$lock_files" ]; then
                echo "  Node$i: removing stale badger LOCK files..."
                echo "$lock_files" | while read -r f; do
                    rm -f "$f"
                    echo "    removed $f"
                done
            fi
        fi
    done

    # Step 4: Wait for key ports to be free
    for i in 1 2 3; do
        local node_ip="${SIMULATED_NETWORK}.$i"
        local attempt=0
        while [ $attempt -lt 10 ]; do
            if ! ss -tlnp 2>/dev/null | grep -q "${node_ip}:${AWSGW_PORT}"; then
                break
            fi
            echo "  Node$i: waiting for port ${AWSGW_PORT} to be free..."
            sleep 1
            attempt=$((attempt + 1))
        done
    done

    echo "  Force cleanup complete"
}

# Global variable for init PID tracking (used by multi-node formation)
LEADER_INIT_PID=""

# Initialize leader node (node1)
# In multi-node mode (default --nodes 3), this backgrounds the init process
# because the formation server blocks waiting for joins.
# Usage: init_leader_node
init_leader_node() {
    echo "Initializing leader node (node1)..."

    # Remove old node directory
    rm -rf "$HOME/node1/"

    # Start init in background — formation server will wait for joins
    ./bin/hive admin init \
        --node node1 \
        --bind "${NODE1_IP}" \
        --cluster-bind "${NODE1_IP}" \
        --cluster-routes "${NODE1_IP}:${NATS_CLUSTER_PORT}" \
        --predastore-nodes "${NODE1_IP},${NODE2_IP},${NODE3_IP}" \
        --port ${CLUSTER_PORT} \
        --region ap-southeast-2 \
        --az ap-southeast-2a \
        --hive-dir "$HOME/node1/" \
        --config-dir "$HOME/node1/config/" &
    LEADER_INIT_PID=$!

    # Wait for formation server to be ready
    echo "Waiting for formation server..."
    for i in $(seq 1 60); do
        if curl -s "http://${NODE1_IP}:${CLUSTER_PORT}/formation/health" > /dev/null 2>&1; then
            echo "  Formation server is ready (PID: $LEADER_INIT_PID)"
            return 0
        fi
        sleep 1
    done

    echo "  ERROR: Formation server failed to start"
    return 1
}

# Join a follower node to the cluster
# Usage: join_follower_node <node_num>
join_follower_node() {
    local node_num="$1"
    local node_ip="${SIMULATED_NETWORK}.$node_num"
    local data_dir="$HOME/node$node_num"

    echo "Joining node$node_num ($node_ip) to cluster..."

    # Remove old node directory
    rm -rf "$data_dir/"

    # Route to node1 (seed node) - other nodes discovered via NATS gossip
    ./bin/hive admin join \
        --node "node$node_num" \
        --bind "$node_ip" \
        --cluster-bind "$node_ip" \
        --cluster-routes "${NODE1_IP}:${NATS_CLUSTER_PORT}" \
        --host "${NODE1_IP}:${CLUSTER_PORT}" \
        --data-dir "$data_dir/" \
        --config-dir "$data_dir/config/" \
        --region ap-southeast-2 \
        --az "ap-southeast-2a"

    echo "Node$node_num joined cluster"
}

# Verify Predastore cluster health
# Checks that Predastore is reachable on all node IPs
# Usage: verify_predastore_cluster [expected_nodes]
verify_predastore_cluster() {
    local expected_nodes="${1:-3}"
    local healthy=0

    echo "Verifying Predastore cluster health (expecting $expected_nodes nodes)..."

    for i in $(seq 1 "$expected_nodes"); do
        local node_ip="${SIMULATED_NETWORK}.$i"

        if curl -k -s "https://${node_ip}:${PREDASTORE_PORT}" > /dev/null 2>&1; then
            echo "  Node$i ($node_ip:${PREDASTORE_PORT}): reachable"
            healthy=$((healthy + 1))
        else
            echo "  Node$i ($node_ip:${PREDASTORE_PORT}): NOT reachable"
        fi
    done

    echo "  Healthy Predastore nodes: $healthy/$expected_nodes"

    if [ "$healthy" -ge "$expected_nodes" ]; then
        echo "  Predastore cluster is healthy"
        return 0
    else
        echo "  WARNING: Predastore cluster may not be fully formed"
        return 1
    fi
}
