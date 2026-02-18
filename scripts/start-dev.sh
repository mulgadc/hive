#!/bin/bash

# Start Hive development environment
# This script starts all required services in the correct order using Hive service commands
# Usage: ./scripts/start-dev.sh [data-dir]
#   data-dir: Optional data directory path (default: ~/hive)
#
# Environment variables:
#   UI=false              Skip starting Hive UI (e.g., UI=false ./scripts/start-dev.sh)
#   HIVE_SKIP_BUILD=true  Skip building binaries before starting

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

# Accept optional data directory argument
DATA_DIR="${1:-$HOME/hive}"

# Configuration paths
# Use CONFIG_DIR environment variable if set, otherwise derive from DATA_DIR
CONFIG_DIR="${CONFIG_DIR:-$DATA_DIR/config}"
echo "Using data directory: $DATA_DIR"
echo "Using configuration directory: $CONFIG_DIR"
LOGS_DIR="$DATA_DIR/logs"
WAL_DIR="$DATA_DIR/hive"

echo "🚀 Starting Hive development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Data directory: $DATA_DIR"

# Parse services from hive.toml — defaults to all if not set
parse_services() {
    local config_file="$CONFIG_DIR/hive.toml"
    if [ -f "$config_file" ]; then
        local svc_line=$(grep -m1 '^services' "$config_file" | sed 's/.*\[//;s/\].*//;s/"//g;s/,/ /g')
        if [ -n "$svc_line" ]; then
            echo "$svc_line"
            return
        fi
    fi
    echo "nats predastore viperblock daemon awsgw ui"
}

SERVICES=$(parse_services)
has_service() {
    local svc="$1"
    echo "$SERVICES" | grep -qw "$svc"
}
echo "Services: $SERVICES"

# Detect multi-node cluster from config
is_multinode() {
    local config_file="$CONFIG_DIR/hive.toml"
    if [ -f "$config_file" ]; then
        local node_count=$(grep -c '^\[nodes\.' "$config_file")
        [ "$node_count" -gt 1 ]
    else
        return 1
    fi
}

# Confirm configuration directory exists
if [ ! -d "$CONFIG_DIR" ]; then
    echo "⚠️  Configuration directory $CONFIG_DIR does not exist."
    echo "Please init the hive environment using the CLI."
    echo "hive admin init"
    exit 1
fi


if [ -d "/mnt/ramdisk" ]; then

    # Check if /mnt/ramdisk is mounted, if not mount it as tmpfs
    if ! mountpoint -q /mnt/ramdisk; then
        echo "💾 Mounting /mnt/ramdisk as tmpfs"
        sudo mount -t tmpfs -o size=8G tmpfs /mnt/ramdisk/
    fi

    # If /mnt/ramdisk is mounted, use it for the WAL directory (for development)
    if mountpoint -q "/mnt/ramdisk"; then
        WAL_DIR="/mnt/ramdisk/"
    fi

else
    echo "⚠️  /mnt/ramdisk not available, using $DATA_DIR/viperblock"
fi

# Change to project root for all commands
cd "$PROJECT_ROOT"

# Function to start service in background
start_service() {
    local name="$1"
    local command="$2"
    local pidfile="$LOGS_DIR/$name.pid"
    local logfile="$LOGS_DIR/$name.log"

    echo "📡 Starting $name..."
    echo "   Command: $command"

    # Start service and capture PID
    nohup $command > "$logfile" 2>&1 &
    local pid=$!
    echo $pid > "$pidfile"

    echo "   PID: $pid, Log: $logfile"

    # Brief pause to let service start
    sleep 2
}

# Function to start service in foreground (for final daemon)
start_service_foreground() {
    local name="$1"
    local command="$2"

    echo "📡 Starting $name in foreground..."
    echo "   Command: $command"

    # Setup signal handler to stop background services when daemon stops
    trap 'echo ""; echo "🛑 Stopping all services..."; ./scripts/stop-dev.sh; exit 0' INT TERM

    # Start the service in foreground
    $command
}

# Function to set OOM score for a service (Linux only)
# Protects infrastructure services from OOM killer (-500 = less likely to be killed)
set_oom_score() {
    local name="$1"
    local score="$2"
    local pid_file="$LOGS_DIR/${name}.pid"
    if [ -f "$pid_file" ]; then
        local pid=$(cat "$pid_file")
        if [ -d "/proc/$pid" ]; then
            echo "$score" > "/proc/$pid/oom_score_adj" 2>/dev/null && \
                echo "  OOM score for $name (PID $pid): $score" || \
                echo "  Warning: Could not set OOM score for $name"
        fi
    fi
}

# Function to check if service is responsive
check_service() {
    local name="$1"
    local host="$2"
    local port="$3"
    local max_attempts=10
    local attempt=1

    echo "🔍 Checking $name connectivity on $host:$port..."

    while [ $attempt -le $max_attempts ]; do
        if nc -z "$host" "$port" 2>/dev/null; then
            echo "   ✅ $name is responding on $host:$port"
            return 0
        fi
        echo "   ⏳ Attempt $attempt/$max_attempts - waiting for $name..."
        sleep 2
        ((attempt++))
    done

    echo "   ⚠️  $name may not be responding on $host:$port (continuing anyway)"

}

# Pre-flight, compile latest
if [ "$HIVE_SKIP_BUILD" != "true" ]; then
    echo "✈️  Pre-flight, compiling latest..."

    echo "   Building hive..."
    make build

    echo "   Building predastore..."
    cd "$MULGA_ROOT/predastore" && make build
    cd "$PROJECT_ROOT"

    echo "   Building viperblock (nbdkit plugin)..."
    cd "$MULGA_ROOT/viperblock" && make build
    cd "$PROJECT_ROOT"

    echo "   ✅ Build complete"
else
    echo "✈️  Skipping build (HIVE_SKIP_BUILD=true)"
fi

# 1️⃣ Start NATS server
echo ""
if has_service "nats"; then
    echo "1️⃣  Starting NATS server..."

    if [ -f "$CONFIG_DIR/nats/nats.conf" ]; then
        export HIVE_CONFIG_PATH=$CONFIG_DIR/nats/nats.conf
        echo " Using NATS config file: $CONFIG_DIR/nats/nats.conf"
    else
        echo " ⚠️ NATS config file not found at $CONFIG_DIR/nats/nats.conf, using defaults"
        export HIVE_NATS_HOST=0.0.0.0
        export HIVE_NATS_PORT=4222
        export HIVE_NATS_DATA_DIR=$DATA_DIR/nats/
        export HIVE_NATS_JETSTREAM=false
    fi

    NATS_CMD="./bin/hive service nats start"
    start_service "nats" "$NATS_CMD"
    set_oom_score "nats" "-500"
else
    echo "1️⃣  Skipping NATS (not a local service)"
fi

# 2️⃣ Start Predastore
echo ""
if has_service "predastore"; then
    echo "2️⃣  Starting Predastore..."
    unset HIVE_CONFIG_PATH

    export HIVE_PREDASTORE_BASE_PATH=$DATA_DIR/predastore/
    export HIVE_PREDASTORE_CONFIG_PATH=$CONFIG_DIR/predastore/predastore.toml
    export HIVE_PREDASTORE_TLS_CERT=$CONFIG_DIR/server.pem
    export HIVE_PREDASTORE_TLS_KEY=$CONFIG_DIR/server.key

    # Auto-detect Predastore host:port from hive.toml [nodes.<name>.predastore] section
    PREDASTORE_BIND="0.0.0.0:8443"
    if [ -f "$CONFIG_DIR/hive.toml" ]; then
        DETECTED_PREDASTORE_HOST=$(awk -F'"' '/\[nodes\..*\.predastore\]/{found=1} found && /^host/{print $2; exit}' "$CONFIG_DIR/hive.toml")
        if [ -n "$DETECTED_PREDASTORE_HOST" ]; then
            PREDASTORE_BIND="$DETECTED_PREDASTORE_HOST"
            echo "   Auto-detected Predastore bind=$PREDASTORE_BIND from hive.toml"
        fi
    fi
    export HIVE_PREDASTORE_HOST="${PREDASTORE_BIND%%:*}"
    export HIVE_PREDASTORE_PORT="${PREDASTORE_BIND##*:}"

    export HIVE_PREDASTORE_BACKEND=distributed

    # Auto-detect Predastore NODE_ID from hive.toml if not already set
    if [ -z "$HIVE_PREDASTORE_NODE_ID" ]; then
        if [ -f "$CONFIG_DIR/hive.toml" ]; then
            DETECTED_NODE_ID=$(awk -F'= *' '/node_id/{gsub(/ /,"",$2); print $2; exit}' "$CONFIG_DIR/hive.toml")
            if [ -n "$DETECTED_NODE_ID" ] && [ "$DETECTED_NODE_ID" != "0" ]; then
                export HIVE_PREDASTORE_NODE_ID="$DETECTED_NODE_ID"
                echo "   Auto-detected Predastore NODE_ID=$DETECTED_NODE_ID from hive.toml"
            fi
        fi
    fi
    export HIVE_PREDASTORE_NODE_ID="${HIVE_PREDASTORE_NODE_ID:-}"

    PREDASTORE_CMD="./bin/hive service predastore start"
    start_service "predastore" "$PREDASTORE_CMD"
    set_oom_score "predastore" "-500"
    check_service "Predastore" "$HIVE_PREDASTORE_HOST" "$HIVE_PREDASTORE_PORT"
else
    echo "2️⃣  Skipping Predastore (not a local service)"
fi


# 3️⃣ Start Viperblock
echo ""
if has_service "viperblock"; then
    echo "3️⃣  Starting Viperblock..."

    # Determine base directory for Viperblock data, dev uses /mnt/ramdisk if available
    if [ -d "/mnt/ramdisk" ] && [ -w "/mnt/ramdisk" ]; then
        VB_BASE_DIR="/mnt/ramdisk/"
    else
        VB_BASE_DIR="$DATA_DIR/viperblock/"
    fi

    # Check if NBD plugin exists
    NBD_PLUGIN_PATH="$MULGA_ROOT/viperblock/lib/nbdkit-viperblock-plugin.so"

    if [ ! -f "$NBD_PLUGIN_PATH" ]; then
        echo "   ⚠️  NBD plugin not found at $NBD_PLUGIN_PATH"
        echo "   Building Viperblock first..."
        cd "$MULGA_ROOT/viperblock"
        make build
        cd "$PROJECT_ROOT"
    fi

    export HIVE_CONFIG_PATH=$CONFIG_DIR/hive.toml
    export HIVE_VIPERBLOCK_PLUGIN_PATH=$NBD_PLUGIN_PATH
    export HIVE_BASE_DIR=$VB_BASE_DIR

    VIPERBLOCK_CMD="./bin/hive service viperblock start"
    start_service "viperblock" "$VIPERBLOCK_CMD"
    set_oom_score "viperblock" "-500"
else
    echo "3️⃣  Skipping Viperblock (not a local service)"
fi

# 4️⃣ Start Hive Gateway/Daemon
echo ""
if has_service "daemon"; then
    echo "4️⃣. Starting Hive Gateway..."

    export HIVE_CONFIG_PATH=$CONFIG_DIR/hive.toml
    export HIVE_BASE_DIR=$DATA_DIR/hive/
    export HIVE_WAL_DIR=$WAL_DIR

    HIVE_CMD="./bin/hive service hive start"
    start_service "hive" "$HIVE_CMD"
    set_oom_score "hive" "-500"
else
    echo "4️⃣. Skipping Hive Gateway (not a local service)"
fi


# 5️⃣ Start AWS Gateway
echo ""
if has_service "awsgw"; then
    echo "5️⃣. Starting AWS Gateway..."

    unset HIVE_NATS_HOST
    unset HIVE_PREDASTORE_HOST
    export HIVE_BASE_DIR=$CONFIG_DIR
    export HIVE_CONFIG_PATH=$CONFIG_DIR/hive.toml
    export HIVE_AWSGW_TLS_CERT=$CONFIG_DIR/server.pem
    export HIVE_AWSGW_TLS_KEY=$CONFIG_DIR/server.key

    AWSGW_CMD="./bin/hive service awsgw start"
    start_service "awsgw" "$AWSGW_CMD"
    set_oom_score "awsgw" "-500"
else
    echo "5️⃣. Skipping AWS Gateway (not a local service)"
fi


# 6️⃣ Start Hive UI (skip with UI=false or if not a local service)
if [ "${UI}" != "false" ] && has_service "ui"; then
    echo ""
    echo "6️⃣. Starting Hive UI..."

    HIVEUI_CMD="./bin/hive service hive-ui start"
    start_service "hive-ui" "$HIVEUI_CMD"
    set_oom_score "hive-ui" "-500"
else
    echo ""
    echo "6️⃣. Skipping Hive UI"
fi


echo ""
echo "🔗 Service endpoints will be:"
if [ "${UI}" != "false" ]; then
    echo "   - Hive UI:       https://localhost:3000"
fi
echo "   - NATS:          nats://localhost:4222"
echo "   - Predastore:    https://localhost:8443"
echo "   - AWS Gateway:   https://localhost:9999"
echo ""
echo "📊 Monitor background service logs:"
echo "   tail -f $LOGS_DIR/*.log"
echo ""
echo "🧪 Test with AWS CLI (once daemon is running):"
echo "   export AWS_PROFILE=hive"
echo "   aws ec2 describe-instances"
echo ""

# For multi-node clusters, check peer daemon health (best-effort)
if is_multinode; then
    echo "Checking cluster peer health..."
    # Extract peer daemon hosts from config (host = "ip:port" under [nodes.X.daemon])
    peer_hosts=$(awk '
        /^\[nodes\./ { node=1; daemon=0 }
        node && /^\[.*\.daemon\]/ { daemon=1 }
        daemon && /^host/ { gsub(/[" ]/, "", $3); print $3; daemon=0 }
    ' "$CONFIG_DIR/hive.toml")

    for peer in $peer_hosts; do
        attempts=0
        max_attempts=5
        while [ $attempts -lt $max_attempts ]; do
            if curl -s --connect-timeout 3 "http://$peer/health" > /dev/null 2>&1; then
                echo "   $peer: healthy"
                break
            fi
            attempts=$((attempts + 1))
            if [ $attempts -lt $max_attempts ]; then
                sleep 2
            fi
        done
        if [ $attempts -ge $max_attempts ]; then
            echo "   $peer: not responding (may still be starting)"
        fi
    done
    echo ""
fi

# This will only be reached if air/daemon exits normally
#echo ""
#echo "🛑 Hive development environment stopped"
