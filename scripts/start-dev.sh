#!/bin/bash

# Start Hive development environment
# This script starts all required services in the correct order using Hive service commands

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

# Configuration paths
CONFIG_DIR="$PROJECT_ROOT/config"
DATA_DIR="$HOME/hive"
LOGS_DIR="$DATA_DIR/logs"
WAL_DIR="$DATA_DIR/hive"

echo "🚀 Starting Hive development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Data directory: $DATA_DIR"

# Create necessary directories
mkdir -p "$DATA_DIR"/{predastore,viperblock,logs}

mkdir -p /mnt/ramdisk 2>/dev/null || echo "⚠️  /mnt/ramdisk not available, using $DATA_DIR/viperblock"

# Check if /mnt/ramdisk is mounted, if not mount it as tmpfs
if ! mountpoint -q /mnt/ramdisk; then
    echo "💾 Mounting /mnt/ramdisk as tmpfs"
    sudo mount -t tmpfs -o size=8G tmpfs /mnt/ramdisk/
fi

# If /mnt/ramdisk is mounted, use it for the WAL directory (for development)
if mountpoint -q "/mnt/ramdisk"; then
    WAL_DIR="/mnt/ramdisk/"
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

# Pre-flight, compile latest
echo "1️✈️ Pre-flight, compiling latest..."
make build

# 1️⃣ Start NATS server
echo ""
echo "1️⃣  Starting NATS server..."

export HIVE_NATS_HOST=0.0.0.0
export HIVE_NATS_PORT=4222
export HIVE_NATS_DATA_DIR=$DATA_DIR/nats/
export HIVE_NATS_JETSTREAM=false
export HIVE_NATS_DEBUG=true

# Use air for hot reloading (dev!)
#NATS_CMD="air -c .air-nats.toml"
NATS_CMD="./bin/hive service nats start"

start_service "nats" "$NATS_CMD"

#start_service "nats" "go run cmd/hive/main.go service nats start"
check_service "NATS" "4222"

# 2️⃣ Start Predastore
echo ""
echo "2️⃣  Starting Predastore..."

export HIVE_PREDASTORE_BASE_PATH=~/hive/predastore/
export HIVE_PREDASTORE_CONFIG_PATH=$CONFIG_DIR/predastore/predastore.toml
export HIVE_PREDASTORE_TLS_CERT=$CONFIG_DIR/server.pem
export HIVE_PREDASTORE_TLS_KEY=$CONFIG_DIR/server.key
export HIVE_PREDASTORE_DEBUG=true
export HIVE_PREDASTORE_HOST=0.0.0.0
export HIVE_PREDASTORE_PORT=8443

# Use air for hot reloading (dev!)
#PREDASTORE_CMD="air -c .air-predastore.toml"
PREDASTORE_CMD="./bin/hive service predastore start"

start_service "predastore" "$PREDASTORE_CMD"
check_service "Predastore" "8443"

# 3️⃣ Start Viperblock
echo ""
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

export HIVE_VIPERBLOCK_S3_HOST=0.0.0.0:8443
export HIVE_VIPERBLOCK_S3_BUCKET=predastore
export HIVE_VIPERBLOCK_S3_REGION=ap-southeast-2
export HIVE_VIPERBLOCK_PLUGIN_PATH=$NBD_PLUGIN_PATH
export HIVE_NATS_HOST=0.0.0.0:4222
export HIVE_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
export HIVE_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
export HIVE_BASE_DIR=$VB_BASE_DIR

#VIPERBLOCK_CMD="air -c .air-viperblock.toml"
VIPERBLOCK_CMD="./bin/hive service viperblock start"

#VIPERBLOCK_CMD="go run cmd/hive/main.go service viperblock start \
#    --nats-host 0.0.0.0:4222 \
#    --access-key AKIAIOSFODNN7EXAMPLE \
#    --secret-key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY \
#    --base-dir $VB_BASE_DIR \
#    --plugin-path $NBD_PLUGIN_PATH"

start_service "viperblock" "$VIPERBLOCK_CMD"

# 4️⃣ Start Hive Gateway/Daemon
echo ""
echo "4️⃣  Starting Hive Gateway..."

# Use the same base directory as Viperblock for consistency
export HIVE_BASE_DIR="$VB_BASE_DIR"
export HIVE_CONFIG_PATH=$CONFIG_DIR/hive.toml
export HIVE_BASE_DIR=$DATA_DIR/hive/
export HIVE_WAL_DIR=$WAL_DIR

#HIVE_CMD="air -c .air-hive.toml"
#HIVE_CMD="./bin/hive service hive start --config config/hive.toml --base-dir $HIVE_BASE_DIR --wal-dir $WAL_DIR"
HIVE_CMD="./bin/hive service hive start"


# 5️⃣ Start AWS Gateway
echo ""
echo "4️5️⃣  Starting AWS Gateway..."

# Use the same base directory as Viperblock for consistency

unset HIVE_NATS_HOST
unset HIVE_PREDASTORE_HOST
export HIVE_CONFIG_PATH=$CONFIG_DIR/hive.toml
export HIVE_AWSGW_HOST="0.0.0.0:9999"
export HIVE_AWSGW_TLS_CERT=$CONFIG_DIR/server.pem
export HIVE_AWSGW_TLS_KEY=$CONFIG_DIR/server.key

#HIVE_CMD="air -c .air-hive.toml"
#HIVE_CMD="./bin/hive service hive start --config config/hive.toml --base-dir $HIVE_BASE_DIR --wal-dir $WAL_DIR"
AWSGW_CMD="./bin/hive service awsgw start"

start_service "awsgw" "$AWSGW_CMD"
check_service "awsgw" "9999"


echo ""
echo "🔗 Service endpoints will be:"
echo "   - NATS:          nats://localhost:4222"
echo "   - Predastore:    https://localhost:8443"
echo "   - AWS Gateway:   https://localhost:9999"
echo ""
echo "📊 Monitor background service logs:"
echo "   tail -f $LOGS_DIR/*.log"
echo ""
echo "🧪 Test with AWS CLI (once daemon is running):"
echo "   aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 describe-instances"
echo ""

# This will only be reached if air/daemon exits normally
#echo ""
#echo "🛑 Hive development environment stopped"