#!/bin/bash

# Stop Hive development environment
# This script stops all services started by start-dev.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOGS_DIR="$PROJECT_ROOT/data/logs"

echo "🛑 Stopping Hive development environment..."

# Function to stop service by PID file
stop_service() {
    local name="$1"
    local pidfile="$LOGS_DIR/$name.pid"

    if [[ -f "$pidfile" ]]; then
        local pid=$(cat "$pidfile" 2>/dev/null)
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            echo "🔻 Stopping $name (PID: $pid)..."

            # Try graceful shutdown first
            kill -TERM "$pid" 2>/dev/null

            # Wait a few seconds for graceful shutdown
            local attempts=5
            while [[ $attempts -gt 0 ]] && kill -0 "$pid" 2>/dev/null; do
                sleep 1
                ((attempts--))
            done

            # Force kill if still running
            if kill -0 "$pid" 2>/dev/null; then
                echo "   ⚠️  Forcing shutdown of $name..."
                kill -KILL "$pid" 2>/dev/null
            fi

            echo "   ✅ $name stopped"
        else
            echo "⚠️  $name process not found (PID: $pid)"
        fi

        # Remove PID file
        rm -f "$pidfile"
    else
        echo "⚠️  No PID file for $name"
    fi
}

# Function to stop services by name (fallback)
stop_by_name() {
    local process_name="$1"
    local service_name="$2"

    local pids=$(pgrep -f "$process_name" 2>/dev/null || true)
    if [[ -n "$pids" ]]; then
        echo "🔻 Found $service_name processes, stopping..."
        echo "$pids" | xargs -r kill -TERM 2>/dev/null || true
        sleep 2
        # Force kill any remaining
        echo "$pids" | xargs -r kill -KILL 2>/dev/null || true
    fi
}

# Stop services in reverse order
echo ""
echo "Stopping services..."

# Stop Hive daemon/gateway first
stop_service "hive"
stop_by_name "hive daemon" "Hive daemon"

# Stop Viperblock
stop_service "viperblock"
stop_by_name "service viperblock" "Viperblock"

# Stop Predastore
stop_service "predastore"
stop_by_name "service predastore" "Predastore"

# Stop NATS
stop_service "nats"
stop_by_name "service nats" "NATS"

# Kill any remaining go run processes related to hive
echo ""
echo "🧹 Cleaning up remaining processes..."
pkill -f "go run cmd/hive/main.go" 2>/dev/null && echo "   ✅ Stopped remaining Go processes" || true

# Stop air if it's running
if pgrep -f "air" >/dev/null 2>&1; then
    pkill -f "air" 2>/dev/null && echo "   ✅ Stopped air hot reloader" || true
fi

echo ""
echo "✅ Hive development environment stopped"

# Show any remaining related processes
remaining=$(ps aux | grep -E "(hive|nats|predastore|viperblock)" | grep -v grep | grep -v "stop-dev.sh" || true)
if [[ -n "$remaining" ]]; then
    echo ""
    echo "⚠️  Some related processes may still be running:"
    echo "$remaining"
fi