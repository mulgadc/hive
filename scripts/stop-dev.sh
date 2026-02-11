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
# Stop Hive UI first (last to start)
stop_service "hive-ui" "$PID_DIR"

# Stop Hive daemon/gateway (it will terminate running instances, unmount nbd devices)
stop_service "hive" "$PID_DIR"

# Wait for all QEMU instances to exit before stopping infrastructure services.
# The daemon sends system_powerdown to VMs during shutdown, but may be killed
# before VMs fully exit. We must wait here so viperblock/predastore aren't
# pulled out from under running VMs.
if pgrep -x qemu-system-x86_64 > /dev/null 2>&1; then
    echo "⏳ Waiting for QEMU instances to exit..."
    timeout=120
    elapsed=0
    while pgrep -x qemu-system-x86_64 > /dev/null 2>&1; do
        if [ $elapsed -ge $timeout ]; then
            echo "⚠️  Timeout waiting for QEMU processes, force killing..."
            pkill -9 -x qemu-system-x86_64 2>/dev/null || true
            sleep 1
            break
        fi
        sleep 1
        ((elapsed++))
    done
    echo "✅ All QEMU instances exited"
fi

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
remaining=$(ps aux | grep -E "(hive|hive-ui|nats|predastore|viperblock)" | grep -v grep | grep -v "stop-dev.sh" || true)
if [[ -n "$remaining" ]]; then
    echo ""
    echo "⚠️  Some related processes may still be running:"
    echo "$remaining"
fi