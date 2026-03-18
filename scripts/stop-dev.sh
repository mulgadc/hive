#!/bin/bash

# Stop Spinifex development environment
# This script stops all services started by start-dev.sh
# Usage: ./scripts/stop-dev.sh
# Note: Services are stopped using PID files, so data-dir is not required

# Accept optional data directory argument
DATA_DIR="${1:-$HOME/spinifex}"

# Always use the logs directory for PID files — start-dev.sh writes shell PIDs
# there, and Go services write PIDs to their own data dirs. This ensures
# consistent stop behavior for both single-node and multi-node setups.
PID_DIR="$DATA_DIR/logs"


SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOGS_DIR="$PROJECT_ROOT/data/logs"

# Check if /mnt/ramdisk is mounted
#if mountpoint -q /mnt/ramdisk; then
    #echo "🛑 Stopping Spinifex development environment..."
    #sudo umount /mnt/ramdisk
#fi

echo "Stopping Spinifex development environment..."

# Parse services from spinifex.toml — defaults to all if not set
CONFIG_DIR="${CONFIG_DIR:-$DATA_DIR/config}"
parse_services() {
    local config_file="$CONFIG_DIR/spinifex.toml"
    if [ -f "$config_file" ]; then
        local svc_line=$(grep -m1 '^services' "$config_file" | sed 's/.*\[//;s/\].*//;s/"//g;s/,/ /g')
        if [ -n "$svc_line" ]; then
            echo "$svc_line"
            return
        fi
    fi
    echo "nats predastore viperblock daemon awsgw vpcd ui"
}

SERVICES=$(parse_services)
has_service() {
    local svc="$1"
    case "$svc" in
        spinifex) svc="daemon" ;;
        spinifex-ui) svc="ui" ;;
    esac
    echo "$SERVICES" | grep -qw "$svc"
}

# Detect multi-node cluster from config
is_multinode() {
    local config_file="$CONFIG_DIR/spinifex.toml"
    if [ -f "$config_file" ]; then
        # Count only top-level node sections [nodes.X], not subsections [nodes.X.Y]
        local node_count=$(grep -cE '^\[nodes\.[^.]+\]' "$config_file")
        [ "$node_count" -gt 1 ]
    else
        return 1
    fi
}

# For multi-node clusters, delegate to coordinated shutdown via NATS.
# Only when called without arguments (default path). When a data-dir is
# explicitly provided (e.g., E2E per-node cleanup), use per-service stop.
if is_multinode && [ -z "$SPINIFEX_FORCE_LOCAL_STOP" ] && [ -z "$1" ]; then
    echo "Multi-node cluster detected. Using coordinated shutdown..."
    echo "  (Set SPINIFEX_FORCE_LOCAL_STOP=1 to force per-service stop)"
    exec $PROJECT_ROOT/bin/spx admin cluster shutdown
fi

# Function to stop service by PID file
stop_service() {
    local name="$1"
    local pidpath="$2"

    echo "🛑 Stopping $name $pidpath..."

    local rc=0
    # Workaround for local development for multi-node config on single instance
    if [ -n "$pidpath" ]; then
        kill -SIGTERM $(cat "$pidpath/$name.pid" 2>/dev/null) 2>/dev/null || rc=$?
        sleep 1
    else
    # Correct graceful shutdown via spx binary, waits for clean exit
        $PROJECT_ROOT/bin/spx service $name stop || rc=$?
    fi

    if [[ $rc -ne 0 ]]; then
        echo "⚠️  Failed to stop $name (exit code $rc)"
        return 1
    else
        echo "✅ $name stopped"
        echo ""
    fi

}

# Stop services in reverse order
echo "Stopping services..."
echo ""
# Stop Spinifex UI first (last to start)
has_service "spinifex-ui" && stop_service "spinifex-ui" "$PID_DIR"

# Stop Spinifex daemon/gateway (it will terminate running instances, unmount nbd devices)
has_service "spinifex" && stop_service "spinifex" "$PID_DIR"

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

# Stop vpcd (VPC daemon)
has_service "vpcd" && stop_service "vpcd" "$PID_DIR"

# Stop AWSGW
has_service "awsgw" && stop_service "awsgw" "$PID_DIR"

# Stop Viperblock
has_service "viperblock" && stop_service "viperblock" "$PID_DIR"

# Stop Predastore
has_service "predastore" && stop_service "predastore" "$PID_DIR"

# Stop NATS
has_service "nats" && stop_service "nats" "$PID_DIR"

# Stop OVN networking (prevents idle CPU burn when spinifex isn't running)
if pidof systemd >/dev/null 2>&1; then
    echo "🛑 Stopping OVN networking..."
    sudo systemctl stop ovn-controller 2>/dev/null && echo "✅ ovn-controller stopped" || true
    if systemctl is-active --quiet ovn-central 2>/dev/null; then
        sudo systemctl stop ovn-central 2>/dev/null && echo "✅ ovn-central stopped" || true
    fi
    sudo systemctl stop openvswitch-switch 2>/dev/null && echo "✅ openvswitch-switch stopped" || true
fi

echo ""
echo "✅ Spinifex development environment stopped"

# Show any remaining spinifex-related processes
remaining=$(pgrep -af '(bin/spx|spinifex-ui|nats-server|predastore|viperblock|vpcd|qemu-system)' | grep -v "stop-dev.sh" || true)
if [[ -n "$remaining" ]]; then
    echo ""
    echo "⚠️  Some related processes may still be running:"
    echo "$remaining"
fi