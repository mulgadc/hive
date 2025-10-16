#!/bin/bash

# Stop Hive development environment
# This script stops all services started by start-dev.sh

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

    echo "🛑 Stopping $name..."

    $PROJECT_ROOT/bin/hive service $name stop

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
stop_service "hive"

# Stop AWSGW
stop_service "awsgw"

# Stop Viperblock
stop_service "viperblock"

# Stop Predastore
stop_service "predastore"

# Stop NATS
stop_service "nats"

echo ""
echo "✅ Hive development environment stopped"

# Show any remaining related processes
remaining=$(ps aux | grep -E "(hive|nats|predastore|viperblock)" | grep -v grep | grep -v "stop-dev.sh" || true)
if [[ -n "$remaining" ]]; then
    echo ""
    echo "⚠️  Some related processes may still be running:"
    echo "$remaining"
fi