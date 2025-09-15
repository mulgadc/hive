#!/bin/bash

# Start Hive development environment
# This script starts all required services in the correct order using Hive service commands

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

# Configuration paths
CONFIG_DIR="$PROJECT_ROOT/config"
DATA_DIR="$PROJECT_ROOT/data"
LOGS_DIR="$DATA_DIR/logs"

echo "🚀 Starting Hive development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Data directory: $DATA_DIR"

# Create necessary directories
mkdir -p "$DATA_DIR"/{predastore,viperblock,logs}
mkdir -p /mnt/ramdisk 2>/dev/null || echo "⚠️  /mnt/ramdisk not available, using $DATA_DIR/viperblock"

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

# Function to check if service is responsive
check_service() {
    local name="$1"
    local port="$2"
    local max_attempts=10
    local attempt=1

    echo "🔍 Checking $name connectivity on port $port..."

    while [ $attempt -le $max_attempts ]; do
        if nc -z localhost "$port" 2>/dev/null; then
            echo "   ✅ $name is responding on port $port"
            return 0
        fi
        echo "   ⏳ Attempt $attempt/$max_attempts - waiting for $name..."
        sleep 2
        ((attempt++))
    done

    echo "   ⚠️  $name may not be responding on port $port (continuing anyway)"
    return 1
}

# 1️⃣ Start NATS server
echo ""
echo "1️⃣  Starting NATS server..."
start_service "nats" "go run cmd/hive/main.go service nats start"
check_service "NATS" "4222"

# 2️⃣ Start Predastore
echo ""
echo "2️⃣  Starting Predastore..."
PREDASTORE_CMD="go run cmd/hive/main.go service predastore \
    --base-path $DATA_DIR/predastore/ \
    --config-path $CONFIG_DIR/predastore/predastore.toml \
    --tls-cert $CONFIG_DIR/server.pem \
    --tls-key $CONFIG_DIR/server.key \
    --debug start"

start_service "predastore" "$PREDASTORE_CMD"
check_service "Predastore" "8443"

# 3️⃣ Start Viperblock
echo ""
echo "3️⃣  Starting Viperblock..."

# Determine base directory for Viperblock data
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

VIPERBLOCK_CMD="go run cmd/hive/main.go service viperblock start \
    --nats-host 0.0.0.0:4222 \
    --access-key AKIAIOSFODNN7EXAMPLE \
    --secret-key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY \
    --base-dir $VB_BASE_DIR \
    --plugin-path $NBD_PLUGIN_PATH"

start_service "viperblock" "$VIPERBLOCK_CMD"

# 4️⃣ Start Hive Gateway/Daemon
echo ""
echo "4️⃣  Starting Hive Gateway..."

# Use the same base directory as Viperblock for consistency
HIVE_BASE_DIR="$VB_BASE_DIR"

# Check if we should use air for hot reloading
if command -v air >/dev/null 2>&1 && [ -f ".air.toml" ]; then
    echo "   🔥 Using air for hot reloading"
    start_service_foreground "hive-air" "air"
else
    echo "   🔨 Starting Hive daemon with go run"

    # Check if config file exists
    if [ ! -f "$CONFIG_DIR/hive.toml" ]; then
        echo "   ⚠️  Config file not found at $CONFIG_DIR/hive.toml"
        echo "   You may need to create this file or use a different config path"
    fi

    # Start Hive daemon directly with go run (in foreground)
    HIVE_CMD="go run cmd/hive/main.go --config $CONFIG_DIR/hive.toml --base-dir $HIVE_BASE_DIR daemon"

    echo ""
    echo "🔗 Service endpoints will be:"
    echo "   - NATS:          nats://localhost:4222"
    echo "   - Predastore:    https://localhost:8443"
    echo "   - Hive Gateway:  https://localhost:9999"
    echo ""
    echo "📊 Monitor background service logs:"
    echo "   tail -f $LOGS_DIR/*.log"
    echo ""
    echo "🧪 Test with AWS CLI (once daemon is running):"
    echo "   aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 describe-instances"
    echo ""

    # Use foreground function which includes signal handling
    start_service_foreground "hive-daemon" "$HIVE_CMD"
fi

# This will only be reached if air/daemon exits normally
echo ""
echo "🛑 Hive development environment stopped"