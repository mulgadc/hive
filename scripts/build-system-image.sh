#!/bin/bash
set -euo pipefail

# build-system-image.sh — Build a minimal Alpine AMI from a manifest
#
# Creates a pre-baked Alpine Linux image with custom packages, binaries,
# and setup scripts installed, ready for import as a Spinifex AMI.
#
# Requirements: qemu-nbd, qemu-img, sudo (for mount/chroot), curl
# Usage: ./scripts/build-system-image.sh <manifest.conf> [--import]
#   --import  Also import the image as an AMI via spx admin images import

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
    echo "Usage: $0 <manifest.conf> [--import]"
    echo ""
    echo "  manifest.conf  Path to image manifest (see scripts/images/ for examples)"
    echo "  --import       Import the built image as an AMI"
    exit 1
}

if [[ $# -lt 1 || "$1" == "-h" || "$1" == "--help" ]]; then
    usage
fi

MANIFEST="$1"
shift

if [[ ! -f "$MANIFEST" ]]; then
    echo "ERROR: Manifest not found: $MANIFEST"
    exit 1
fi

DO_IMPORT=false
QUIET=false
for arg in "$@"; do
    case "$arg" in
        --import) DO_IMPORT=true ;;
        --quiet)  QUIET=true ;;
    esac
done

# Source the manifest
# shellcheck source=/dev/null
source "$MANIFEST"

# Validate required manifest fields
for field in IMAGE_NAME ALPINE_VERSION IMAGE_SIZE; do
    if [[ -z "${!field:-}" ]]; then
        echo "ERROR: Manifest missing required field: $field"
        exit 1
    fi
done

# Derived paths
BUILD_DIR="/tmp/${IMAGE_NAME}-image-build"
ALPINE_IMAGE="generic_alpine-${ALPINE_VERSION}-x86_64-bios-cloudinit-r0.qcow2"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v${ALPINE_VERSION%.*}/releases/cloud/${ALPINE_IMAGE}"
OUTPUT_IMAGE="${BUILD_DIR}/${IMAGE_NAME}-alpine.qcow2"
OUTPUT_RAW="${BUILD_DIR}/${IMAGE_NAME}-alpine.raw"
NBD_DEV="/dev/nbd0"
MOUNT_DIR="${BUILD_DIR}/mnt"

cleanup() {
    echo "Cleaning up..."
    sudo umount "${MOUNT_DIR}" 2>/dev/null || true
    sudo qemu-nbd --disconnect "${NBD_DEV}" 2>/dev/null || true
    exec 9>&- 2>/dev/null || true  # release nbd lock if held
    echo "Done."
}
trap cleanup EXIT

# In quiet mode, redirect build output to /dev/null (import output still shown)
if [[ "$QUIET" == true ]]; then
    exec 3>&1         # save original stdout
    exec 1>/dev/null  # suppress build output
fi

echo "=== System Image Builder ==="
echo "Image:   ${IMAGE_NAME} — ${IMAGE_DESCRIPTION:-}"
echo "Alpine:  ${ALPINE_VERSION}"
echo "Size:    ${IMAGE_SIZE}"
echo "Build:   ${BUILD_DIR}"
echo ""

# Step 0: Check prerequisites
if ! command -v qemu-nbd &>/dev/null; then
    echo "ERROR: qemu-nbd not found. Install qemu-utils."
    exit 1
fi

if ! command -v qemu-img &>/dev/null; then
    echo "ERROR: qemu-img not found. Install qemu-utils."
    exit 1
fi

# Build binaries if BUILD_COMMANDS is set
if [[ -n "${BUILD_COMMANDS:-}" ]]; then
    echo "Building binaries: ${BUILD_COMMANDS}"
    if ! (cd "$PROJECT_DIR" && eval "$BUILD_COMMANDS"); then
        echo "ERROR: BUILD_COMMANDS failed: ${BUILD_COMMANDS}"
        exit 1
    fi
fi

# Verify binaries exist and are statically linked
if [[ -n "${INSTALL_BINARIES:-}" ]]; then
    IFS=' ' read -ra BINARY_PAIRS <<< "$INSTALL_BINARIES"
    for pair in "${BINARY_PAIRS[@]}"; do
        src="${pair%%:*}"
        src_path="${PROJECT_DIR}/${src}"
        if [[ ! -f "$src_path" ]]; then
            echo "ERROR: Binary not found: $src_path"
            exit 1
        fi
        if ! file "$src_path" | grep -q "statically linked"; then
            echo "ERROR: $src_path is not statically linked (Alpine uses musl — dynamic glibc binaries will fail)"
            echo "  Rebuild with: CGO_ENABLED=0 go build ..."
            exit 1
        fi
    done
fi

# Serialize the entire image build with flock — concurrent builds on the same
# host (e.g. CI single-node + multi-node jobs) share /dev/nbd0 and BUILD_DIR.
NBD_LOCK="/tmp/build-system-image.lock"
echo "Acquiring build lock..."
exec 9>"$NBD_LOCK"
flock 9
echo "Lock acquired"

# If the raw image was built recently (< 10 min), skip the entire build.
# This avoids duplicate work when concurrent CI jobs build the same image.
if [[ -f "$OUTPUT_RAW" ]] && [[ $(( $(date +%s) - $(stat -c %Y "$OUTPUT_RAW") )) -lt 600 ]]; then
    echo "=== Skipping build — $OUTPUT_RAW is fresh (< 10 min old) ==="

    # Restore stdout if suppressed, then jump to import
    if [[ "$QUIET" == true ]]; then
        exec 1>&3 3>&-
    fi

    echo ""
    echo "=== Build complete (cached) ==="
    echo "  raw: $OUTPUT_RAW ($(du -h "$OUTPUT_RAW" | cut -f1))"

    if [[ "$DO_IMPORT" == true ]]; then
        echo "Importing as AMI..."
        (cd "$PROJECT_DIR" && ./bin/spx admin images import \
            --file "$OUTPUT_RAW" \
            --distro alpine \
            --version "${ALPINE_VERSION}-${IMAGE_NAME}" \
            --arch x86_64)
    fi
    exit 0
fi

mkdir -p "$BUILD_DIR" "$MOUNT_DIR"

# Step 1: Download Alpine cloud image
if [[ -f "${BUILD_DIR}/${ALPINE_IMAGE}" ]]; then
    echo "Alpine image already downloaded."
else
    echo "Downloading Alpine ${ALPINE_VERSION} cloud image..."
    if ! curl --fail -L -o "${BUILD_DIR}/${ALPINE_IMAGE}" "$ALPINE_URL"; then
        rm -f "${BUILD_DIR}/${ALPINE_IMAGE}"
        echo "ERROR: Failed to download Alpine image from $ALPINE_URL"
        exit 1
    fi
    # Verify the download is a valid qcow2 image
    if ! qemu-img info "${BUILD_DIR}/${ALPINE_IMAGE}" &>/dev/null; then
        rm -f "${BUILD_DIR}/${ALPINE_IMAGE}"
        echo "ERROR: Downloaded file is not a valid qcow2 image"
        exit 1
    fi
fi

# Step 2: Copy image for customization
rm -f "$OUTPUT_IMAGE"
echo "Copying image for customization..."
cp "${BUILD_DIR}/${ALPINE_IMAGE}" "$OUTPUT_IMAGE"

# Resize the image (Alpine cloud images are ~200MB, need room for packages)
qemu-img resize "$OUTPUT_IMAGE" "$IMAGE_SIZE"

# Step 3: Connect via qemu-nbd
echo "Connecting image via qemu-nbd..."
sudo modprobe nbd max_part=4 2>/dev/null || true
if [[ ! -e "${NBD_DEV}" ]]; then
    echo "ERROR: ${NBD_DEV} does not exist. Is the nbd kernel module loaded? Try: sudo modprobe nbd"
    exit 1
fi
sudo qemu-nbd --disconnect "${NBD_DEV}" 2>/dev/null || true
sudo qemu-nbd --connect="${NBD_DEV}" "$OUTPUT_IMAGE"

# Wait for the nbd device to be ready (kernel needs time to scan the block device)
for i in $(seq 1 10); do
    if sudo blockdev --getsize64 "${NBD_DEV}" &>/dev/null; then
        break
    fi
    sleep 1
done
sleep 1

# Alpine cloud images have ext4 directly on the block device (no partition table).
# Resize the filesystem to fill the resized image.
echo "Checking filesystem..."
sudo e2fsck -f -y "${NBD_DEV}" || {
    ec=$?
    if [[ $ec -gt 1 ]]; then
        echo "ERROR: e2fsck failed with exit code $ec on ${NBD_DEV}"
        exit 1
    fi
}

echo "Resizing filesystem..."
if ! sudo resize2fs "${NBD_DEV}"; then
    echo "ERROR: resize2fs failed on ${NBD_DEV}"
    exit 1
fi

# Step 4: Mount and customize
echo "Mounting root filesystem..."
sudo mount "${NBD_DEV}" "$MOUNT_DIR"

# Set up resolv.conf for DNS inside chroot
sudo cp /etc/resolv.conf "${MOUNT_DIR}/etc/resolv.conf"

# Install packages
if [[ -n "${APK_PACKAGES:-}" ]]; then
    echo "Installing packages: ${APK_PACKAGES}..."
    sudo chroot "$MOUNT_DIR" /bin/sh -c "
set -e
# Enable community repo
sed -i 's|^#\(.*community\)|\1|' /etc/apk/repositories 2>/dev/null || true

# Ensure community repo is present
if ! grep -q community /etc/apk/repositories; then
    MIRROR=\$(grep main /etc/apk/repositories | head -1 | sed 's|/main|/community|')
    echo \"\$MIRROR\" >> /etc/apk/repositories
fi

apk update
apk add ${APK_PACKAGES}
"
fi

# Enable OpenRC services
if [[ -n "${ENABLE_SERVICES:-}" ]]; then
    echo "Enabling services: ${ENABLE_SERVICES}..."
    IFS=' ' read -ra SERVICES <<< "$ENABLE_SERVICES"
    for svc in "${SERVICES[@]}"; do
        if ! sudo chroot "$MOUNT_DIR" /bin/sh -c "rc-update add ${svc} default"; then
            echo "ERROR: Failed to enable service '${svc}' — does it exist in the image?"
            exit 1
        fi
    done
fi

# Step 5: Copy binaries into the image (before setup script, which may reference them)
if [[ -n "${INSTALL_BINARIES:-}" ]]; then
    echo "Installing binaries..."
    IFS=' ' read -ra BINARY_PAIRS <<< "$INSTALL_BINARIES"
    for pair in "${BINARY_PAIRS[@]}"; do
        src="${pair%%:*}"
        dst="${pair#*:}"
        src_path="${PROJECT_DIR}/${src}"
        echo "  ${src} -> ${dst}"
        sudo cp "$src_path" "${MOUNT_DIR}${dst}"
        sudo chmod 755 "${MOUNT_DIR}${dst}"
    done
fi

# Run custom setup script inside chroot (after binaries are installed)
if [[ -n "${SETUP_SCRIPT:-}" ]]; then
    setup_path="${PROJECT_DIR}/${SETUP_SCRIPT}"
    if [[ ! -f "$setup_path" ]]; then
        echo "ERROR: Setup script not found: $setup_path"
        exit 1
    fi
    echo "Running setup script: ${SETUP_SCRIPT}..."
    sudo cp "$setup_path" "${MOUNT_DIR}/tmp/setup.sh"
    sudo chmod 755 "${MOUNT_DIR}/tmp/setup.sh"
    sudo chroot "$MOUNT_DIR" /tmp/setup.sh
    sudo rm -f "${MOUNT_DIR}/tmp/setup.sh"
fi

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

# Restore stdout if suppressed
if [[ "$QUIET" == true ]]; then
    exec 1>&3 3>&-
fi

echo ""
echo "=== Build complete ==="
echo "  Image: ${IMAGE_NAME}"
echo "  qcow2: $OUTPUT_IMAGE ($(du -h "$OUTPUT_IMAGE" | cut -f1))"
echo "  raw:   $OUTPUT_RAW ($(du -h "$OUTPUT_RAW" | cut -f1))"
echo ""

if [[ "$DO_IMPORT" == true ]]; then
    echo "Importing as AMI..."
    (cd "$PROJECT_DIR" && sudo -u spinifex-storage ./bin/spx admin images import \
        --file "$OUTPUT_RAW" \
        --distro alpine \
        --version "${ALPINE_VERSION}-${IMAGE_NAME}" \
        --arch x86_64)
else
    echo "To import as AMI, run:"
    echo "  cd $PROJECT_DIR && ./bin/spx admin images import \\"
    echo "    --file $OUTPUT_RAW \\"
    echo "    --distro alpine --version ${ALPINE_VERSION}-${IMAGE_NAME} --arch x86_64"
fi
