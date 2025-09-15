#!/bin/bash

# Hive Service Installation Script
# This script installs the hive systemd service on Ubuntu/Debian systems

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   print_error "This script must be run as root (use sudo)"
   exit 1
fi

# Check if systemd is available
if ! command -v systemctl &> /dev/null; then
    print_error "systemd is not available on this system"
    exit 1
fi

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_FILE="$SCRIPT_DIR/hive.service"

# Check if service file exists
if [[ ! -f "$SERVICE_FILE" ]]; then
    print_error "Service file not found at $SERVICE_FILE"
    exit 1
fi

print_status "Installing Hive systemd service..."

# Copy service file to systemd directory
cp "$SERVICE_FILE" /etc/systemd/system/

# Reload systemd to recognize the new service
systemctl daemon-reload

# Enable the service to start on boot
systemctl enable hive.service

print_status "Service installed successfully!"
print_status "You can now use the following commands:"
echo "  systemctl start hive    # Start the service"
echo "  systemctl stop hive     # Stop the service"
echo "  systemctl status hive   # Check service status"
echo "  systemctl restart hive  # Restart the service"
echo "  journalctl -u hive -f   # View service logs"

# Check if hive binary exists
if [[ ! -f "/opt/hive/bin/hive" ]]; then
    print_warning "Hive binary not found at /opt/hive/bin/hive"
    print_warning "Please ensure hive is installed in /opt/hive before starting the service"
fi

print_status "Installation complete!" 