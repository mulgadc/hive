#!/bin/bash

# Stop Spinifex development environment
# This script stops all services started by start-dev.sh
# Usage: ./scripts/stop-dev.sh
# Note: Services are stopped using PID files, so data-dir is not required

set -uo pipefail

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
if is_multinode && [ -z "${SPINIFEX_FORCE_LOCAL_STOP:-}" ] && [ -z "${1:-}" ]; then
    echo "Multi-node cluster detected. Using coordinated shutdown..."
    echo "  (Set SPINIFEX_FORCE_LOCAL_STOP=1 to force per-service stop)"
    exec $PROJECT_ROOT/bin/spx admin cluster shutdown
fi

# Function to stop a service. Sends SIGTERM and waits for the process to exit
# so resources (ports, DB locks) are fully released before returning.
stop_service() {
    local name="$1"
    local pidpath="$2"

    echo "🛑 Stopping $name..."

    local pid
    pid=$(cat "$pidpath/$name.pid" 2>/dev/null) || { echo "⚠️  No PID file for $name"; return 1; }

    kill -SIGTERM "$pid" 2>/dev/null || { rm -f "$pidpath/$name.pid"; echo "✅ $name already stopped"; echo ""; return 0; }

    # Wait for process to fully exit (up to 15s) so ports/locks are released
    local checks=0
    while kill -0 "$pid" 2>/dev/null; do
        sleep 1
        checks=$((checks + 1))
        if [ "$checks" -ge 15 ]; then
            echo "  Force killing $name..."
            kill -9 "$pid" 2>/dev/null || true
            sleep 1
            break
        fi
    done

    rm -f "$pidpath/$name.pid"
    echo "✅ $name stopped"
    echo ""
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
# CRITICAL: Do NOT stop openvswitch-switch if OVS carries WAN traffic.
# This can happen when an OVS bridge has a WAN IP (direct bridge mode) or
# when a veth pair links an OVS bridge to a Linux WAN bridge (veth mode).
# Stopping OVS would destroy the bridge and break WAN connectivity.
# OVS is infrastructure — only stop ovn-controller and ovn-central.
if pidof systemd >/dev/null 2>&1; then
    echo "🛑 Stopping OVN networking..."
    sudo systemctl stop ovn-controller 2>/dev/null && echo "✅ ovn-controller stopped" || true
    if systemctl is-active --quiet ovn-central 2>/dev/null; then
        sudo systemctl stop ovn-central 2>/dev/null && echo "✅ ovn-central stopped" || true
    fi

    # Only stop OVS if tearing it down won't kill WAN connectivity.
    # Two cases where OVS is load-bearing for WAN:
    #   1. An OVS bridge has a WAN IP directly (direct bridge mode)
    #   2. A veth pair links a Linux bridge to an OVS bridge (veth mode) —
    #      stopping OVS destroys the OVS bridge end, breaking the link
    WAN_BRIDGE_HAS_IP=false
    VETH_LINKS_WAN=false
    for br in $(sudo ovs-vsctl list-br 2>/dev/null); do
        if [ "$br" = "br-int" ]; then continue; fi
        if ip -4 addr show "$br" 2>/dev/null | grep -q "inet "; then
            WAN_BRIDGE_HAS_IP=true
            break
        fi
    done
    # Check if a veth pair links an OVS bridge to a Linux bridge with a WAN IP
    if ip link show veth-wan-br >/dev/null 2>&1; then
        VETH_LINKS_WAN=true
    fi
    if [ "$WAN_BRIDGE_HAS_IP" = true ]; then
        echo "⚠️  Skipping openvswitch-switch stop ($br has WAN IP — stopping would kill connectivity)"
    elif [ "$VETH_LINKS_WAN" = true ]; then
        echo "⚠️  Skipping openvswitch-switch stop (veth pair links OVS to WAN bridge)"
    else
        sudo systemctl stop openvswitch-switch 2>/dev/null && echo "✅ openvswitch-switch stopped" || true
    fi
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