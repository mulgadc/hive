#!/bin/bash
# dev-install.sh — Full local development setup via production installer.
# Builds from source, assembles a tarball, runs setup.sh for scaffolding
# (user creation, directories, systemd units), initializes the cluster,
# and starts services via systemd.
#
# Usage: ./scripts/dev-install.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MULGA_ROOT="$(cd "$PROJECT_ROOT/.." && pwd)"

echo "=== Building ==="
cd "$PROJECT_ROOT" && make build
cd "$MULGA_ROOT/viperblock" && make go_build_nbd

echo "=== Assembling tarball for setup.sh ==="
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT
cp "$PROJECT_ROOT/bin/spx" "$STAGING/spx"
cp "$MULGA_ROOT/viperblock/lib/nbdkit-viperblock-plugin.so" "$STAGING/"
cp "$PROJECT_ROOT/scripts/setup-ovn.sh" "$STAGING/"
mkdir -p "$STAGING/systemd"
cp "$PROJECT_ROOT/build/systemd/"* "$STAGING/systemd/"
mkdir -p "$STAGING/scripts"
cp "$PROJECT_ROOT/build/scripts/"* "$STAGING/scripts/"
cp "$PROJECT_ROOT/build/logrotate/spinifex" "$STAGING/logrotate-spinifex"
tar czf /tmp/spinifex-local.tar.gz -C "$STAGING" .

echo "=== Cleaning stale state from previous installs ==="
# Stop any running services before modifying files
sudo systemctl stop spinifex.target 2>/dev/null || true
sudo systemctl reset-failed 2>/dev/null || true

# Remove stale files owned by the dev user in production paths.
# Previous dev-mode installs (admin init without sudo, start-dev.sh) leave
# files owned by tf-user that service users (spinifex-nats, etc.) can't read
# under systemd's ProtectSystem=strict sandboxing.
for dir in /var/lib/spinifex /var/log/spinifex /etc/spinifex; do
    if [ -d "$dir" ]; then
        # Remove PID files, stale logs, and the legacy ~/spinifex/config symlink
        sudo find "$dir" -name '*.pid' -delete 2>/dev/null || true
        sudo find "$dir" -name '*.log' -delete 2>/dev/null || true
        sudo find "$dir" -name '*.log.*' -delete 2>/dev/null || true
    fi
done
# Remove legacy data dir contents that conflict with production layout
if [ -d /var/lib/spinifex/config ]; then
    sudo rm -rf /var/lib/spinifex/config
fi

echo "=== Running setup.sh (creates users, dirs, systemd units) ==="
sudo INSTALL_SPINIFEX_TARBALL=/tmp/spinifex-local.tar.gz bash "$PROJECT_ROOT/scripts/setup.sh"
rm -f /tmp/spinifex-local.tar.gz

echo "=== Setting up OVN ==="
sudo /usr/local/share/spinifex/setup-ovn.sh --management

echo "=== Initializing ==="
sudo spx admin init --force --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1

echo "=== Installing CA certificate ==="
sudo cp /etc/spinifex/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates

echo "=== Starting services ==="
sudo systemctl start spinifex.target

echo "=== Done ==="
echo "Services: sudo systemctl status spinifex.target"
echo "Logs:     journalctl -u 'spinifex-*' -f"
echo "Test:     AWS_PROFILE=spinifex aws ec2 describe-instances"
echo "Iterate:  make deploy (rebuild + restart all)"
