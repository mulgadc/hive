#!/bin/bash

# Hive Platform Development Environment Setup
# This script sets up a complete development environment for the Hive platform

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

# Default configuration
NATS_PORT=4222
PREDASTORE_PORT=8443
HIVE_GATEWAY_PORT=9999
DATA_DIR="$PROJECT_ROOT/data"
CONFIG_DIR="$PROJECT_ROOT/config"

echo "🚀 Setting up Hive development environment..."
echo "Project root: $PROJECT_ROOT"
echo "Data directory: $DATA_DIR"

# Create necessary directories
mkdir -p "$DATA_DIR"/{nats,predastore,viperblock,logs}
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
required_commands=("go" "make" "nats-server")
for cmd in "${required_commands[@]}"; do
    if command_exists "$cmd"; then
        echo "✅ $cmd found"
    else
        echo "❌ $cmd not found. Please install $cmd"
        case "$cmd" in
            "nats-server")
                echo "   Install NATS: https://docs.nats.io/running-a-nats-service/introduction/installation"
                ;;
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
                echo "   Install air for hot reloading: go install github.com/cosmtrek/air@latest"
                ;;
            "nbdkit")
                echo "   Install nbdkit: sudo apt-get install nbdkit (Ubuntu/Debian)"
                ;;
        esac
    fi
done

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

ports=("$NATS_PORT:NATS" "$PREDASTORE_PORT:Predastore" "$HIVE_GATEWAY_PORT:Hive Gateway")
for port_info in "${ports[@]}"; do
    port="${port_info%:*}"
    service="${port_info#*:}"
    if port_available "$port"; then
        echo "✅ Port $port available for $service"
    else
        echo "⚠️  Port $port already in use (needed for $service)"
    fi
done

# Generate TLS certificates for development
echo ""
echo "🔐 Setting up TLS certificates..."

if [[ ! -f "$CONFIG_DIR/server.pem" ]] || [[ ! -f "$CONFIG_DIR/server.key" ]]; then
    echo "📋 Generating self-signed certificate for development..."
    openssl req -x509 -newkey rsa:4096 -keyout "$CONFIG_DIR/server.key" -out "$CONFIG_DIR/server.pem" \
        -days 365 -nodes -subj "/C=US/ST=Dev/L=Development/O=Hive/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1"
    echo "✅ TLS certificates generated"
else
    echo "✅ TLS certificates already exist"
fi

# Create development configuration
echo ""
echo "⚙️  Creating development configuration..."

cat > "$CONFIG_DIR/dev.yaml" << EOF
# Hive Development Configuration
server:
  host: "0.0.0.0"
  port: $HIVE_GATEWAY_PORT
  tls:
    cert_file: "$CONFIG_DIR/server.pem"
    key_file: "$CONFIG_DIR/server.key"

nats:
  host: "nats://localhost:$NATS_PORT"
  acl:
    token: "dev-token"

services:
  predastore:
    host: "https://localhost:$PREDASTORE_PORT"
    access_key: "EXAMPLEKEY"
    secret_key: "EXAMPLEKEY"
    bucket: "predastore"
    region: "ap-southeast-2"

  viperblock:
    base_dir: "$DATA_DIR/viperblock"

data:
  base_dir: "$DATA_DIR"

logging:
  level: "info"
  format: "json"
EOF

echo "✅ Development configuration created at $CONFIG_DIR/dev.yaml"

# Create air configuration for hot reloading
if command_exists "air"; then
    echo ""
    echo "🔥 Setting up air configuration for hot reloading..."

    cat > "$PROJECT_ROOT/.air.toml" << EOF
# Air configuration for Hive development
root = "."
testdata_dir = "testdata"
tmp_dir = "tmp"

[build]
  args_bin = ["daemon", "--config", "config/dev.yaml"]
  bin = "./tmp/hive"
  cmd = "go build -o ./tmp/hive cmd/hive/main.go"
  delay = 1000
  exclude_dir = ["assets", "tmp", "vendor", "testdata", "data", "nbdkit"]
  exclude_file = []
  exclude_regex = ["_test.go"]
  exclude_unchanged = false
  follow_symlink = false
  full_bin = ""
  include_dir = []
  include_ext = ["go", "tpl", "tmpl", "html"]
  kill_delay = "0s"
  log = "build-errors.log"
  send_interrupt = false
  stop_on_root = false

[color]
  app = ""
  build = "yellow"
  main = "magenta"
  runner = "green"
  watcher = "cyan"

[log]
  time = false

[misc]
  clean_on_exit = false

[screen]
  clear_on_rebuild = false
EOF

    echo "✅ Air configuration created"
fi

# Create startup script
echo ""
echo "📝 Creating startup scripts..."

cat > "$PROJECT_ROOT/scripts/start-dev.sh" << 'EOF'
#!/bin/bash

# Start Hive development environment
# This script starts all required services in the correct order

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

echo "🚀 Starting Hive development environment..."

# Function to start service in background
start_service() {
    local name="$1"
    local command="$2"
    local pidfile="$PROJECT_ROOT/data/logs/$name.pid"
    local logfile="$PROJECT_ROOT/data/logs/$name.log"

    echo "📡 Starting $name..."
    nohup $command > "$logfile" 2>&1 &
    echo $! > "$pidfile"
    echo "   PID: $(cat $pidfile), Log: $logfile"
}

# Create logs directory
mkdir -p "$PROJECT_ROOT/data/logs"

# Start services in dependency order
echo ""
echo "1️⃣  Starting NATS server..."
start_service "nats" "nats-server --port 4222 --auth dev-token"

echo ""
echo "2️⃣  Starting Predastore..."
cd "$MULGA_ROOT/predastore"
start_service "predastore" "./bin/s3d --port 8443 --data-dir $PROJECT_ROOT/data/predastore"

echo ""
echo "3️⃣  Starting NBDkit (if available)..."
if command -v nbdkit >/dev/null; then
    start_service "nbdkit" "nbdkit --foreground --verbose memory 1G"
else
    echo "   ⚠️  NBDkit not available, skipping"
fi

echo ""
echo "4️⃣  Starting Hive Gateway..."
cd "$PROJECT_ROOT"
if command -v air >/dev/null; then
    echo "   🔥 Using air for hot reloading"
    air
else
    echo "   🔨 Building and starting Hive"
    make build
    ./bin/hive daemon --config config/dev.yaml
fi
EOF

chmod +x "$PROJECT_ROOT/scripts/start-dev.sh"

# Create stop script
cat > "$PROJECT_ROOT/scripts/stop-dev.sh" << 'EOF'
#!/bin/bash

# Stop Hive development environment

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "🛑 Stopping Hive development environment..."

# Function to stop service
stop_service() {
    local name="$1"
    local pidfile="$PROJECT_ROOT/data/logs/$name.pid"

    if [[ -f "$pidfile" ]]; then
        local pid=$(cat "$pidfile")
        if kill -0 "$pid" 2>/dev/null; then
            echo "🔻 Stopping $name (PID: $pid)..."
            kill "$pid"
            rm -f "$pidfile"
        else
            echo "⚠️  $name process not found (PID: $pid)"
            rm -f "$pidfile"
        fi
    else
        echo "⚠️  No PID file for $name"
    fi
}

# Stop services in reverse order
stop_service "hive"
stop_service "nbdkit"
stop_service "predastore"
stop_service "nats"

echo "✅ Development environment stopped"
EOF

chmod +x "$PROJECT_ROOT/scripts/stop-dev.sh"

echo "✅ Startup scripts created"

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

# Build Hive
echo "📦 Building Hive..."
cd "$PROJECT_ROOT"
make build
echo "✅ Hive built successfully"

echo ""
echo "🎉 Development environment setup complete!"
echo ""
echo "🚀 To start the development environment:"
echo "   ./scripts/start-dev.sh"
echo ""
echo "🛑 To stop the development environment:"
echo "   ./scripts/stop-dev.sh"
echo ""
echo "🔧 Development endpoints:"
echo "   - Hive Gateway:  https://localhost:$HIVE_GATEWAY_PORT"
echo "   - Predastore S3: https://localhost:$PREDASTORE_PORT"
echo "   - NATS:          nats://localhost:$NATS_PORT"
echo ""
echo "📊 Monitor logs:"
echo "   tail -f data/logs/*.log"
echo ""
echo "🧪 Test with AWS CLI:"
echo "   aws --endpoint-url https://localhost:$HIVE_GATEWAY_PORT --no-verify-ssl ec2 describe-instances"