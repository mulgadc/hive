#!/bin/bash

# Stop Hive development environment
# This script stops all services started by start-dev.sh
# Usage: ./scripts/stop-dev.sh
# Note: Services are stopped using PID files, so data-dir is not required

# Accept optional data directory argument
DATA_DIR="${1:-$HOME/hive}"

# If path is provided, use for pid location, else the default for Hive
if [ -n "$1" ]; then
    PID_DIR="$DATA_DIR/logs"
else
    PID_DIR=""
fi


SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOGS_DIR="$PROJECT_ROOT/data/logs"

# Check if /mnt/ramdisk is mounted
#if mountpoint -q /mnt/ramdisk; then
    #echo "🛑 Stopping Hive development environment..."
    #sudo umount /mnt/ramdisk
#fi

echo "Stopping Hive development environment..."

# Function to stop service by PID file
stop_service() {
    local name="$1"
    local pidpath="$2"

    echo "🛑 Stopping $name $pidpath..."

    # Workaround for local development for multi-node config on single instance
    if [ -n "$pidpath" ]; then
        kill -SIGTERM `cat $pidpath/$name.pid`
        sleep 1
    else
    # Correct graceful shutdown via hive binary, waits for clean exit
        $PROJECT_ROOT/bin/hive service $name stop
    fi

    echo "Status: $?"

    if [[ $? -ne 0 ]]; then
        echo "⚠️  Failed to stop $name"
        return 1
    else
        echo "✅ $name stopped"
        echo ""
    fi

}

# Stop services in reverse order
echo "Stopping services..."
echo ""
# Stop Hive daemon/gateway first (it will terminate running instances, unmount nbd devices)
stop_service "hive" "$PID_DIR"

# Stop AWSGW
stop_service "awsgw" "$PID_DIR"

# Stop Viperblock
stop_service "viperblock" "$PID_DIR"

# Stop Predastore
stop_service "predastore" "$PID_DIR"

# Stop NATS
stop_service "nats" "$PID_DIR"

echo ""
echo "✅ Hive development environment stopped"

# Show any remaining related processes
remaining=$(ps aux | grep -E "(hive|nats|predastore|viperblock)" | grep -v grep | grep -v "stop-dev.sh" || true)
if [[ -n "$remaining" ]]; then
    echo ""
    echo "⚠️  Some related processes may still be running:"
    echo "$remaining"
fi