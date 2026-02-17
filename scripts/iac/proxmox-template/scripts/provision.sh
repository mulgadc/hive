#!/bin/bash
# provision.sh — Installs all Hive dependencies, clones repos, caches Go modules,
# and generalizes the VM for use as a Proxmox template.
#
# Runs as tf-user via Terraform remote-exec. Uses sudo for system operations.
# Environment: GIT_BRANCH (default: main)

set -euo pipefail

GIT_BRANCH="${GIT_BRANCH:-main}"
WORK_DIR="$HOME/Development/mulga"

echo "========================================"
echo "  Hive Template Provisioning"
echo "  Branch: ${GIT_BRANCH}"
echo "  Arch:   $(uname -m)"
echo "========================================"

# ── 1. Wait for cloud-init ──────────────────────────────────────────────────
echo ""
echo "=== [1/8] Waiting for cloud-init to complete ==="
cloud-init status --wait

# ── 2. Clone repositories ───────────────────────────────────────────────────
echo ""
echo "=== [2/8] Cloning repositories ==="
mkdir -p "$WORK_DIR"
cd "$WORK_DIR"

for repo in hive viperblock predastore; do
    if [ ! -d "$repo" ]; then
        echo "  Cloning $repo (branch: $GIT_BRANCH)..."
        git clone -b "$GIT_BRANCH" "https://github.com/mulgadc/${repo}.git"
    else
        echo "  $repo already exists, skipping"
    fi
done

# ── 3. Install system dependencies via Makefile ─────────────────────────────
echo ""
echo "=== [3/8] Installing system dependencies ==="
cd "$WORK_DIR/hive"
sudo make quickinstall

# Ensure Go is in PATH for the rest of the script
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
echo "  Go version: $(go version)"
echo "  AWS CLI:    $(aws --version)"

# ── 4. Set up go.work ───────────────────────────────────────────────────────
echo ""
echo "=== [4/8] Creating go.work ==="

# Detect Go version from go.mod
GO_VERSION=$(grep '^go ' "$WORK_DIR/hive/go.mod" | awk '{print $2}')
echo "  Using Go version from go.mod: ${GO_VERSION}"

cat > "$WORK_DIR/hive/go.work" << GOWORK
go ${GO_VERSION}

use (
    .
    ../viperblock
    ../viperblock/nbd
    ../viperblock/nbd/libguestfs.org/nbdkit
    ../predastore
)
GOWORK

echo "  Created $WORK_DIR/hive/go.work"

# ── 5. Download Go modules ──────────────────────────────────────────────────
echo ""
echo "=== [5/8] Downloading Go modules ==="
cd "$WORK_DIR/hive"       && go mod download && echo "  hive: done"
cd "$WORK_DIR/viperblock"  && go mod download && echo "  viperblock: done"
cd "$WORK_DIR/predastore"  && go mod download && echo "  predastore: done"

# Sub-modules
if [ -f "$WORK_DIR/viperblock/nbd/go.mod" ]; then
    cd "$WORK_DIR/viperblock/nbd" && go mod download && echo "  viperblock/nbd: done"
fi
if [ -f "$WORK_DIR/viperblock/nbd/libguestfs.org/nbdkit/go.mod" ]; then
    cd "$WORK_DIR/viperblock/nbd/libguestfs.org/nbdkit" && go mod download && echo "  viperblock/nbd/nbdkit: done"
fi

# ── 6. Pre-download cloud images for nested VMs ─────────────────────────────
echo ""
echo "=== [6/8] Pre-downloading cloud images ==="
mkdir -p "$HOME/images"

ARCH=$(dpkg --print-architecture)
IMAGE_URL=""
if [ "$ARCH" = "amd64" ]; then
    IMAGE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
elif [ "$ARCH" = "arm64" ]; then
    IMAGE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img"
fi

if [ -n "$IMAGE_URL" ]; then
    echo "  Downloading Ubuntu 24.04 cloud image ($ARCH)..."
    curl -L -o "$HOME/images/ubuntu-24.04.img" "$IMAGE_URL"
    echo "  Saved to ~/images/ubuntu-24.04.img"
else
    echo "  Unknown arch ($ARCH), skipping cloud image download"
fi

# ── 7. System tuning ────────────────────────────────────────────────────────
echo ""
echo "=== [7/8] Applying system tuning ==="

# Persistent sysctl for NATS / high-throughput networking
sudo tee /etc/sysctl.d/99-hive.conf > /dev/null << 'SYSCTL'
net.core.rmem_max=4194304
net.core.wmem_max=4194304
SYSCTL
echo "  sysctl: net.core.rmem_max/wmem_max = 4MB"

# Add user to kvm group for QEMU access
sudo usermod -aG kvm "$(whoami)"
echo "  Added $(whoami) to kvm group"

# Disable ufw if present
if command -v ufw >/dev/null 2>&1; then
    sudo ufw disable 2>/dev/null || true
    echo "  ufw disabled"
fi

# ── 8. Generalize for templating ────────────────────────────────────────────
echo ""
echo "=== [8/8] Cleaning up for template ==="

# Apt caches
sudo apt-get clean
sudo rm -rf /var/lib/apt/lists/*
echo "  Cleared apt cache"

# Logs
sudo find /var/log -type f -name "*.log" -exec truncate -s 0 {} \; 2>/dev/null || true
sudo rm -rf /var/log/journal/*
echo "  Truncated logs"

# Temp files
sudo rm -rf /tmp/* /var/tmp/*

# Machine ID — systemd regenerates on next boot.
# This is critical: without this, all clones get the same DHCP client ID → IP conflicts.
sudo truncate -s 0 /etc/machine-id
sudo rm -f /var/lib/dbus/machine-id
echo "  Cleared machine-id (will regenerate on boot)"

# SSH host keys — regenerated on boot by cloud-init or systemd
sudo rm -f /etc/ssh/ssh_host_*
echo "  Removed SSH host keys (will regenerate on boot)"

# Cloud-init state — forces cloud-init to run again on next boot.
# This ensures each clone gets its own hostname, SSH keys, user config, etc.
sudo cloud-init clean
echo "  Cleaned cloud-init state (will re-run on boot)"

# Shell history
history -c 2>/dev/null || true
cat /dev/null > "$HOME/.bash_history" 2>/dev/null || true

# Provisioning script itself
rm -f /tmp/provision.sh

# Sync filesystem
sync

echo ""
echo "========================================"
echo "  Template provisioning complete!"
echo "  VM is ready to be shut down and"
echo "  converted to a Proxmox template."
echo "========================================"
