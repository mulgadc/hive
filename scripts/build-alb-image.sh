#!/bin/bash
set -euo pipefail

# build-alb-image.sh — Build a minimal Alpine AMI for ALB VMs
#
# Creates a pre-baked Alpine Linux image with HAProxy + alb-agent installed,
# ready for import as a Spinifex AMI. ALB VMs boot in ~3s and need no internet
# access or package installation at runtime.
#
# Requirements: qemu-nbd, sudo (for mount/chroot), curl
# Usage: ./scripts/build-alb-image.sh [--import]
#   --import  Also import the image as an AMI via spx admin images import

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="/tmp/alb-image-build"
ALPINE_VERSION="3.21.6"
ALPINE_IMAGE="generic_alpine-${ALPINE_VERSION}-x86_64-bios-cloudinit-r0.qcow2"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v3.21/releases/cloud/${ALPINE_IMAGE}"
OUTPUT_IMAGE="${BUILD_DIR}/alb-alpine.qcow2"
OUTPUT_RAW="${BUILD_DIR}/alb-alpine.raw"
NBD_DEV="/dev/nbd0"
MOUNT_DIR="${BUILD_DIR}/mnt"
ALB_AGENT_BIN="${PROJECT_DIR}/bin/alb-agent"

DO_IMPORT=false
if [[ "${1:-}" == "--import" ]]; then
    DO_IMPORT=true
fi

cleanup() {
    echo "Cleaning up..."
    sudo umount "${MOUNT_DIR}" 2>/dev/null || true
    sudo qemu-nbd --disconnect "${NBD_DEV}" 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT

echo "=== ALB Alpine Image Builder ==="
echo "Alpine version: ${ALPINE_VERSION}"
echo "Build dir: ${BUILD_DIR}"
echo ""

# Step 0: Check prerequisites
if [ ! -f "$ALB_AGENT_BIN" ]; then
    echo "Building alb-agent (static)..."
    (cd "$PROJECT_DIR" && CGO_ENABLED=0 go build -o bin/alb-agent cmd/alb-agent/main.go)
else
    # Verify the existing binary is statically linked
    if ! file "$ALB_AGENT_BIN" | grep -q "statically linked"; then
        echo "Rebuilding alb-agent as static binary (Alpine uses musl)..."
        (cd "$PROJECT_DIR" && CGO_ENABLED=0 go build -o bin/alb-agent cmd/alb-agent/main.go)
    fi
fi

if ! command -v qemu-nbd &>/dev/null; then
    echo "ERROR: qemu-nbd not found. Install qemu-utils."
    exit 1
fi

mkdir -p "$BUILD_DIR" "$MOUNT_DIR"

# Step 1: Download Alpine cloud image
if [ -f "${BUILD_DIR}/${ALPINE_IMAGE}" ]; then
    echo "Alpine image already downloaded."
else
    echo "Downloading Alpine ${ALPINE_VERSION} cloud image..."
    curl -L -o "${BUILD_DIR}/${ALPINE_IMAGE}" "$ALPINE_URL"
fi

# Step 2: Copy image for customization
echo "Copying image for customization..."
cp "${BUILD_DIR}/${ALPINE_IMAGE}" "$OUTPUT_IMAGE"

# Resize the image to 512M (Alpine cloud images are ~200MB, need room for packages)
qemu-img resize "$OUTPUT_IMAGE" 512M

# Step 3: Connect via qemu-nbd
echo "Connecting image via qemu-nbd..."
sudo modprobe nbd max_part=4 2>/dev/null || true
sudo qemu-nbd --disconnect "${NBD_DEV}" 2>/dev/null || true
sudo qemu-nbd --connect="${NBD_DEV}" "$OUTPUT_IMAGE"
sleep 1

# Alpine cloud images have ext4 directly on the block device (no partition table).
# Resize the filesystem to fill the resized image.
echo "Resizing filesystem..."
sudo e2fsck -f -y "${NBD_DEV}" 2>/dev/null || true
sudo resize2fs "${NBD_DEV}" 2>/dev/null || true

# Step 4: Mount and customize
echo "Mounting root filesystem..."
sudo mount "${NBD_DEV}" "$MOUNT_DIR"

# Set up resolv.conf for DNS inside chroot
sudo cp /etc/resolv.conf "${MOUNT_DIR}/etc/resolv.conf"

echo "Installing packages in chroot..."
sudo chroot "$MOUNT_DIR" /bin/sh -c '
set -e
# Enable community repo (haproxy is in community)
sed -i "s|^#\(.*community\)|\1|" /etc/apk/repositories 2>/dev/null || true

# Ensure community repo is present
if ! grep -q "community" /etc/apk/repositories; then
    MIRROR=$(grep "main" /etc/apk/repositories | head -1 | sed "s|/main|/community|")
    echo "$MIRROR" >> /etc/apk/repositories
fi

apk update
apk add haproxy curl

# Enable haproxy to start at boot (but do not start it now)
rc-update add haproxy default 2>/dev/null || true

# Create haproxy config directory
mkdir -p /etc/haproxy
cat > /etc/haproxy/haproxy.cfg <<EOF
# Placeholder config — replaced by alb-agent on first config push
global
    daemon
    maxconn 256

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend health
    bind *:8405
    http-request return status 200 content-type text/plain string "ok"
EOF

# Create alb-agent service directory
mkdir -p /etc/init.d
'

# Step 5: Copy alb-agent binary into the image
echo "Installing alb-agent binary..."
sudo cp "$ALB_AGENT_BIN" "${MOUNT_DIR}/usr/local/bin/alb-agent"
sudo chmod 755 "${MOUNT_DIR}/usr/local/bin/alb-agent"

# Create OpenRC init script for alb-agent
sudo tee "${MOUNT_DIR}/etc/init.d/alb-agent" > /dev/null <<'INITSCRIPT'
#!/sbin/openrc-run

description="ALB NATS Config Agent"
command="/usr/local/bin/alb-agent"
command_args="--lb-id=${ALB_LB_ID:-unknown} --nats=${ALB_NATS_URL:-nats://10.0.2.2:4222}"
command_background=true
pidfile="/run/alb-agent.pid"
output_log="/var/log/alb-agent.log"
error_log="/var/log/alb-agent.log"

depend() {
    need net
    after firewall
}
INITSCRIPT
sudo chmod 755 "${MOUNT_DIR}/etc/init.d/alb-agent"

# Create a cloud-init module-final script hook that starts alb-agent with the
# correct LB ID and NATS URL (passed via cloud-init user-data env vars).
# Cloud-init on Alpine runs runcmd which we'll use to start the agent.

# Step 6: Clean up and unmount
echo "Cleaning up image..."
sudo chroot "$MOUNT_DIR" /bin/sh -c '
apk cache clean 2>/dev/null || true
rm -rf /var/cache/apk/* /tmp/*
'

# Restore original resolv.conf (cloud-init will set it on boot)
sudo rm -f "${MOUNT_DIR}/etc/resolv.conf"

echo "Unmounting..."
sudo umount "$MOUNT_DIR"
sudo qemu-nbd --disconnect "${NBD_DEV}"

# Step 7: Convert to raw for import
echo "Converting to raw format..."
qemu-img convert -f qcow2 -O raw "$OUTPUT_IMAGE" "$OUTPUT_RAW"

RAW_SIZE=$(stat -c%s "$OUTPUT_RAW")
echo ""
echo "=== Build complete ==="
echo "  qcow2: $OUTPUT_IMAGE ($(du -h "$OUTPUT_IMAGE" | cut -f1))"
echo "  raw:   $OUTPUT_RAW ($(du -h "$OUTPUT_RAW" | cut -f1))"
echo ""

if [ "$DO_IMPORT" = true ]; then
    echo "Importing as AMI..."
    (cd "$PROJECT_DIR" && ./bin/spx admin images import \
        --file "$OUTPUT_RAW" \
        --distro alpine \
        --version "${ALPINE_VERSION}-alb" \
        --arch x86_64 \
        --config "$HOME/spinifex/config/spinifex.toml")
else
    echo "To import as AMI, run:"
    echo "  cd $PROJECT_DIR && ./bin/spx admin images import \\"
    echo "    --file $OUTPUT_RAW \\"
    echo "    --distro alpine --version ${ALPINE_VERSION}-alb --arch x86_64 \\"
    echo "    --config \$HOME/spinifex/config/spinifex.toml"
fi
