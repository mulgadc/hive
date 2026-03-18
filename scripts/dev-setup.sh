#!/bin/bash

# Spinifex Platform Development Environment Setup
# This script sets up a complete development environment for the Spinifex platform

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

# Default configuration
NATS_PORT=4222
PREDASTORE_PORT=8443
SPINIFEX_GATEWAY_PORT=9999
DATA_DIR="$HOME/spinifex"
# Use CONFIG_DIR environment variable if set, otherwise default to ~/spinifex/config
CONFIG_DIR="${CONFIG_DIR:-$HOME/spinifex/config}"

echo "🚀 Setting up Spinifex development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Data directory: $DATA_DIR"

# Create necessary directories
mkdir -p "$DATA_DIR"/{nats,predastore,viperblock,logs,spinifex}
mkdir -p "$CONFIG_DIR"

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to check if port is available
port_available() {
    ! nc -z localhost "$1" 2>/dev/null
}

# Check dependencies
echo "🔍 Checking dependencies..."

# Check for required commands
required_commands=("go" "make")
for cmd in "${required_commands[@]}"; do
    if command_exists "$cmd"; then
        echo "✅ $cmd found"
    else
        echo "❌ $cmd not found. Please install $cmd"
        case "$cmd" in
            "go")
                echo "   Install Go: https://golang.org/dl/"
                ;;
        esac
    fi
done

# Check optional commands
optional_commands=("air" "nbdkit")
for cmd in "${optional_commands[@]}"; do
    if command_exists "$cmd"; then
        echo "✅ $cmd found (optional)"
    else
        echo "⚠️  $cmd not found (optional)"
        case "$cmd" in
            "air")
                echo "   Install air for hot reloading: go install github.com/air-verse/air@latest"
                ;;
            "nbdkit")
                echo "   Install nbdkit: sudo apt-get install nbdkit (Ubuntu/Debian)"
                ;;
        esac
    fi
done

# Check OVN/OVS commands (required for VPC networking)
echo ""
echo "🔍 Checking OVN/OVS dependencies..."
ovn_commands=("ovs-vsctl" "ovn-controller" "ovn-nbctl")
ovn_ready=true
for cmd in "${ovn_commands[@]}"; do
    if command_exists "$cmd"; then
        echo "✅ $cmd found"
    else
        echo "⚠️  $cmd not found (required for VPC networking)"
        ovn_ready=false
    fi
done
if [ "$ovn_ready" = false ]; then
    echo "   Install OVN: sudo apt-get install ovn-central ovn-host openvswitch-switch"
    echo "   Or run: make quickinstall"
fi

# Initialize br-int if OVS is available
if command_exists "ovs-vsctl"; then
    echo ""
    echo "🌐 Checking OVS br-int bridge..."
    if ovs-vsctl br-exists br-int 2>/dev/null; then
        echo "✅ br-int already exists"
    else
        echo "⚠️  br-int not found. Run ./scripts/setup-ovn.sh to initialize OVN networking"
    fi
fi

# Check component repositories
echo ""
echo "📦 Checking component repositories..."

components=("viperblock" "predastore")
for component in "${components[@]}"; do
    component_path="$MULGA_ROOT/$component"
    if [[ -d "$component_path" ]]; then
        echo "✅ $component found at $component_path"
        if [[ -f "$component_path/go.mod" ]]; then
            echo "   Go module verified"
        else
            echo "   ⚠️  No go.mod found in $component"
        fi
    else
        echo "❌ $component not found at $component_path"
        echo "   Run: ./scripts/clone-deps.sh"
    fi
done

# Check port availability
echo ""
echo "🔌 Checking port availability..."

ports=("$NATS_PORT:NATS" "$PREDASTORE_PORT:Predastore" "$SPINIFEX_GATEWAY_PORT:Spinifex Gateway")
for port_info in "${ports[@]}"; do
    port="${port_info%:*}"
    service="${port_info#*:}"
    if port_available "$port"; then
        echo "✅ Port $port available for $service"
    else
        echo "⚠️  Port $port already in use (needed for $service)"
    fi
done

# Build Spinifex first (needed for admin init)
echo ""
echo "🔨 Building Spinifex..."
cd "$PROJECT_ROOT"
make build
echo "✅ Spinifex built successfully"

# Initialize Spinifex configuration using admin init
#echo ""
#echo "🔐 Initializing Spinifex configuration..."

#if [[ ! -f "$CONFIG_DIR/spinifex.toml" ]]; then
#    echo "📋 Running spx admin init..."
#    ./bin/spx admin init --config-dir "$CONFIG_DIR"
#    echo "✅ Spinifex configuration initialized"
#else
#    echo "✅ Spinifex configuration already exists"
#    echo "   To re-initialize, run: ./bin/spx admin init --force"
#fi

# Build components
echo ""
echo "🔨 Building components..."

# Build Viperblock
if [[ -d "$MULGA_ROOT/viperblock" ]]; then
    echo "📦 Building Viperblock..."
    cd "$MULGA_ROOT/viperblock"
    make build
    echo "✅ Viperblock built successfully"
else
    echo "⚠️  Viperblock directory not found, skipping build"
fi

# Build Predastore
if [[ -d "$MULGA_ROOT/predastore" ]]; then
    echo "📦 Building Predastore..."
    cd "$MULGA_ROOT/predastore"
    make build
    echo "✅ Predastore built successfully"
else
    echo "⚠️  Predastore directory not found, skipping build"
fi

echo ""
echo "🎉 Development environment setup complete!"
echo ""
echo "When running Spinifex for the first time, run the init function to create the"
echo "default directories for data, config files and layout required."
echo "./bin/spx admin init"
echo ""
echo "🚀 To start the development environment:"
echo "   ./scripts/start-dev.sh"
echo ""
echo "🛑 To stop the development environment:"
echo "   ./scripts/stop-dev.sh"
echo ""
echo "🔧 Development endpoints:"
echo "   - Spinifex Gateway:  https://localhost:$SPINIFEX_GATEWAY_PORT"
echo "   - Predastore S3: https://localhost:$PREDASTORE_PORT"
echo "   - NATS:          nats://localhost:$NATS_PORT"
echo ""
echo "📊 Monitor logs:"
echo "   tail -f $DATA_DIR/logs/*.log"
echo ""
echo "🧪 Test with AWS CLI:"
echo "   aws --endpoint-url https://localhost:$SPINIFEX_GATEWAY_PORT --no-verify-ssl ec2 describe-instances"
