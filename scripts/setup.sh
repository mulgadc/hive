#!/bin/bash
# Spinifex binary installer
# Usage: curl -sfL https://install.mulgadc.com | bash
#
# Environment variables:
#   INSTALL_SPINIFEX_CHANNEL   Release channel: latest (default), dev
#   INSTALL_SPINIFEX_VERSION   Pin to specific version (overrides channel)
#   INSTALL_SPINIFEX_TARBALL   Path to local tarball (skips download, for testing/air-gapped)
#   INSTALL_SPINIFEX_SKIP_APT  Set to 1 to skip apt dependency install
#   INSTALL_SPINIFEX_SKIP_AWS  Set to 1 to skip AWS CLI install

set -e

INSTALL_SPINIFEX_CHANNEL="${INSTALL_SPINIFEX_CHANNEL:-latest}"
INSTALL_BASE_URL="${INSTALL_BASE_URL:-https://install.mulgadc.com}"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fatal() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# --- Sudo setup ---
setup_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        SUDO=""
    elif command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
        if ! $SUDO -n true 2>/dev/null; then
            info "This installer requires sudo access for system-level operations"
            $SUDO true || fatal "Failed to obtain sudo access"
        fi
    else
        fatal "This script requires root or sudo access"
    fi
}

# --- OS detection ---
detect_os() {
    if [ ! -f /etc/os-release ]; then
        fatal "Cannot detect OS: /etc/os-release not found"
    fi

    . /etc/os-release

    case "$ID" in
        debian)
            if [ "${VERSION_ID%%.*}" -lt 12 ] 2>/dev/null; then
                fatal "Debian $VERSION_ID is not supported. Minimum: Debian 12"
            fi
            ;;
        ubuntu)
            major="${VERSION_ID%%.*}"
            if [ "$major" -lt 22 ] 2>/dev/null; then
                fatal "Ubuntu $VERSION_ID is not supported. Minimum: Ubuntu 22.04"
            fi
            ;;
        *)
            fatal "Unsupported OS: $ID $VERSION_ID. Spinifex requires Debian 12+ or Ubuntu 22.04+"
            ;;
    esac

    info "Detected OS: $PRETTY_NAME"
}

# --- Architecture detection ---
detect_arch() {
    MACHINE=$(uname -m)
    case "$MACHINE" in
        x86_64)
            ARCH="amd64"
            QEMU_PACKAGES="qemu-system-x86"
            AWS_ARCH="x86_64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            QEMU_PACKAGES="qemu-system-arm"
            AWS_ARCH="aarch64"
            ;;
        *)
            fatal "Unsupported architecture: $MACHINE. Spinifex requires x86_64 or aarch64"
            ;;
    esac

    info "Detected architecture: $MACHINE ($ARCH)"
}

# --- Detect service user ---
detect_service_user() {
    # Use the real user — either the current user, or SUDO_USER if running via sudo
    SPINIFEX_USER="${SUDO_USER:-$(whoami)}"
    SPINIFEX_GROUP="$(id -gn "$SPINIFEX_USER")"

    if [ "$SPINIFEX_USER" = "root" ]; then
        fatal "Cannot determine service user. Do not run as root directly — use: curl ... | bash (as a normal user)"
    fi

    info "Services will run as: $SPINIFEX_USER:$SPINIFEX_GROUP"

    # Allow the service user to manage OVN/OVS and tap devices
    $SUDO tee /etc/sudoers.d/spinifex-network > /dev/null << SUDOERS
# Spinifex VPC networking: allow service user to manage tap devices, OVS, and OVN
${SPINIFEX_USER} ALL=(root) NOPASSWD: /sbin/ip, /usr/sbin/ip
${SPINIFEX_USER} ALL=(root) NOPASSWD: /usr/bin/ovs-vsctl, /usr/bin/ovs-appctl
${SPINIFEX_USER} ALL=(root) NOPASSWD: /usr/bin/ovn-nbctl, /usr/bin/ovn-sbctl
SUDOERS
    $SUDO chmod 0440 /etc/sudoers.d/spinifex-network
    info "Sudoers rules installed for $SPINIFEX_USER"
}

# --- Install apt dependencies ---
install_apt_deps() {
    if [ "${INSTALL_SPINIFEX_SKIP_APT}" = "1" ]; then
        info "Skipping apt dependencies (INSTALL_SPINIFEX_SKIP_APT=1)"
        return
    fi

    info "Installing system dependencies..."
    $SUDO apt-get update -qq

    DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y -qq \
        nbdkit \
        $QEMU_PACKAGES qemu-utils qemu-kvm \
        libvirt-daemon-system libvirt-clients \
        jq curl iproute2 netcat-openbsd wget unzip xz-utils file \
        ovn-central ovn-host openvswitch-switch dhcpcd-base \
        > /dev/null

    info "System dependencies installed"
}

# --- Install AWS CLI ---
install_aws_cli() {
    if [ "${INSTALL_SPINIFEX_SKIP_AWS}" = "1" ]; then
        info "Skipping AWS CLI (INSTALL_SPINIFEX_SKIP_AWS=1)"
        return
    fi

    if command -v aws >/dev/null 2>&1; then
        info "AWS CLI already installed: $(aws --version 2>&1 | head -1)"
        return
    fi

    info "Installing AWS CLI v2..."
    AWS_TMPDIR=$(mktemp -d)
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${AWS_ARCH}.zip" -o "$AWS_TMPDIR/awscliv2.zip"
    unzip -q "$AWS_TMPDIR/awscliv2.zip" -d "$AWS_TMPDIR"
    $SUDO "$AWS_TMPDIR/aws/install" --update > /dev/null
    rm -rf "$AWS_TMPDIR"

    info "AWS CLI installed: $(aws --version 2>&1 | head -1)"
}

# --- Download tarball ---
download_spinifex() {
    SPINIFEX_TMPDIR=$(mktemp -d)
    TARBALL="$SPINIFEX_TMPDIR/spinifex.tar.gz"

    # Local tarball override — skip download (for testing and air-gapped installs)
    if [ -n "$INSTALL_SPINIFEX_TARBALL" ]; then
        info "Using local tarball: $INSTALL_SPINIFEX_TARBALL"
        cp "$INSTALL_SPINIFEX_TARBALL" "$TARBALL"
        info "Extracting..."
        tar -xzf "$TARBALL" -C "$SPINIFEX_TMPDIR"
        EXTRACT_DIR="$SPINIFEX_TMPDIR"
        return
    fi

    if [ -n "$INSTALL_SPINIFEX_VERSION" ]; then
        DOWNLOAD_URL="${INSTALL_BASE_URL}/download/${INSTALL_SPINIFEX_VERSION}/${ARCH}"
        info "Downloading Spinifex $INSTALL_SPINIFEX_VERSION ($ARCH)..."
    else
        DOWNLOAD_URL="${INSTALL_BASE_URL}/download/${INSTALL_SPINIFEX_CHANNEL}/${ARCH}"
        info "Downloading Spinifex ($INSTALL_SPINIFEX_CHANNEL channel, $ARCH)..."
    fi

    HTTP_CODE=$(curl -fsSL -w '%{http_code}' -o "$TARBALL" "$DOWNLOAD_URL" 2>/dev/null) || true
    if [ ! -f "$TARBALL" ] || [ "$HTTP_CODE" -ge 400 ] 2>/dev/null; then
        rm -rf "$SPINIFEX_TMPDIR"
        fatal "Failed to download Spinifex from $DOWNLOAD_URL (HTTP $HTTP_CODE)"
    fi

    # Verify checksum if available
    CHECKSUM_URL="${DOWNLOAD_URL}.sha256"
    if curl -fsSL -o "$SPINIFEX_TMPDIR/checksum.sha256" "$CHECKSUM_URL" 2>/dev/null; then
        info "Verifying checksum..."
        EXPECTED=$(awk '{print $1}' "$SPINIFEX_TMPDIR/checksum.sha256")
        ACTUAL=$(sha256sum "$TARBALL" | awk '{print $1}')
        if [ "$EXPECTED" != "$ACTUAL" ]; then
            rm -rf "$SPINIFEX_TMPDIR"
            fatal "Checksum verification failed. Expected: $EXPECTED, Got: $ACTUAL"
        fi
        info "Checksum verified"
    else
        warn "Checksum not available, skipping verification"
    fi

    # Extract
    info "Extracting..."
    tar -xzf "$TARBALL" -C "$SPINIFEX_TMPDIR"
    EXTRACT_DIR="$SPINIFEX_TMPDIR"
}

# --- Place files ---
install_files() {
    info "Installing files..."

    # Binary
    $SUDO install -m 0755 "$EXTRACT_DIR/spx" /usr/local/bin/spx
    info "  /usr/local/bin/spx"

    # nbdkit plugin
    PLUGINDIR=$(nbdkit --dump-config 2>/dev/null | grep ^plugindir= | cut -d= -f2)
    if [ -z "$PLUGINDIR" ]; then
        warn "Could not detect nbdkit plugin directory, using default"
        if [ "$ARCH" = "arm64" ]; then
            PLUGINDIR="/usr/lib/aarch64-linux-gnu/nbdkit/plugins"
        else
            PLUGINDIR="/usr/lib/x86_64-linux-gnu/nbdkit/plugins"
        fi
    fi
    $SUDO mkdir -p "$PLUGINDIR"
    $SUDO install -m 0755 "$EXTRACT_DIR/nbdkit-viperblock-plugin.so" "$PLUGINDIR/nbdkit-viperblock-plugin.so"
    info "  $PLUGINDIR/nbdkit-viperblock-plugin.so"

    # Setup scripts
    $SUDO mkdir -p /usr/local/share/spinifex
    if [ -f "$EXTRACT_DIR/setup-ovn.sh" ]; then
        $SUDO install -m 0755 "$EXTRACT_DIR/setup-ovn.sh" /usr/local/share/spinifex/setup-ovn.sh
        info "  /usr/local/share/spinifex/setup-ovn.sh"
    fi
}

# --- Create directories ---
create_directories() {
    info "Creating directories..."

    $SUDO mkdir -p /etc/spinifex
    $SUDO chmod 0700 /etc/spinifex
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /etc/spinifex

    $SUDO mkdir -p /var/lib/spinifex
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /var/lib/spinifex

    # Symlink so services that expect BaseDir/config/ can find /etc/spinifex/
    if [ ! -e /var/lib/spinifex/config ]; then
        $SUDO ln -s /etc/spinifex /var/lib/spinifex/config
    fi

    # Symlink so services that write logs to BaseDir/logs/ use /var/log/spinifex/
    if [ ! -e /var/lib/spinifex/logs ]; then
        $SUDO ln -s /var/log/spinifex /var/lib/spinifex/logs
    fi

    $SUDO mkdir -p /var/log/spinifex
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /var/log/spinifex

    $SUDO mkdir -p /run/spinifex
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /run/spinifex

    $SUDO mkdir -p /var/lib/spinifex/viperblock
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /var/lib/spinifex/viperblock

    # Generate environment file with install-specific values (e.g. arch-dependent paths)
    $SUDO tee /etc/spinifex/systemd.env > /dev/null << EOF
# Generated by setup.sh — install-specific environment variables
SPINIFEX_VIPERBLOCK_PLUGIN_PATH=${PLUGINDIR}/nbdkit-viperblock-plugin.so
EOF
    $SUDO chown "$SPINIFEX_USER:$SPINIFEX_GROUP" /etc/spinifex/systemd.env
    info "Generated /etc/spinifex/systemd.env"
}

# --- Install systemd units ---
install_systemd() {
    info "Installing systemd units..."

    if [ ! -d "$EXTRACT_DIR/systemd" ]; then
        fatal "Systemd unit files not found in tarball (expected systemd/ directory)"
    fi

    for unit in "$EXTRACT_DIR"/systemd/*; do
        # Substitute User= and Group= with the detected service user
        sed "s/^User=spinifex$/User=$SPINIFEX_USER/;s/^Group=spinifex$/Group=$SPINIFEX_GROUP/" \
            "$unit" | $SUDO tee "/etc/systemd/system/$(basename "$unit")" > /dev/null
        $SUDO chmod 0644 "/etc/systemd/system/$(basename "$unit")"
        info "  /etc/systemd/system/$(basename "$unit")"
    done

    $SUDO systemctl daemon-reload
    info "Systemd units installed (running as $SPINIFEX_USER:$SPINIFEX_GROUP)"
}

# --- Install logrotate ---
install_logrotate() {
    if [ -f "$EXTRACT_DIR/logrotate-spinifex" ]; then
        $SUDO install -m 0644 "$EXTRACT_DIR/logrotate-spinifex" /etc/logrotate.d/spinifex
    else
        warn "Logrotate config not found in tarball, skipping"
        return
    fi
    info "Logrotate config installed"
}

# --- Upgrade handling ---
handle_upgrade() {
    if $SUDO systemctl is-active --quiet spinifex.target 2>/dev/null; then
        warn "Spinifex services are running. Stopping for upgrade..."
        $SUDO systemctl stop spinifex.target
        RESTART_AFTER=true
    fi
}

restart_if_needed() {
    if [ "${RESTART_AFTER}" = "true" ]; then
        info "Restarting Spinifex services..."
        $SUDO systemctl start spinifex.target
    fi
}

# --- Print summary ---
print_summary() {
    INSTALLED_VERSION=$(/usr/local/bin/spx version 2>/dev/null || echo "unknown")

    echo ""
    echo "============================================"
    echo "  Spinifex installed successfully"
    echo "============================================"
    echo ""
    echo "  Version:      $INSTALLED_VERSION"
    echo "  Architecture: $ARCH"
    echo "  Service user: $SPINIFEX_USER"
    echo "  Binary:       /usr/local/bin/spx"
    echo "  Config:       /etc/spinifex/"
    echo "  Data:         /var/lib/spinifex/"
    echo "  Logs:         /var/log/spinifex/"
    echo ""
    echo "  Next steps:"
    echo ""
    echo "  1. Setup OVN networking:"
    echo "     sudo /usr/local/share/spinifex/setup-ovn.sh --management"
    echo "     # If WAN is a physical NIC (not a bridge), add:"
    echo "     #   --wan-bridge=br-wan --wan-iface=<WAN_NIC>   (dedicated WAN NIC)"
    echo "     #   --macvlan --wan-iface=<WAN_NIC>              (single-NIC, SSH-safe)"
    echo ""
    echo "  2. Initialize a region:"
    echo "     sudo spx admin init --region <region> --az <az> --node node1 --nodes 1"
    echo ""
    echo "  3. Trust the CA certificate:"
    echo "     sudo cp /etc/spinifex/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt"
    echo "     sudo update-ca-certificates"
    echo ""
    echo "  4. Start services:"
    echo "     sudo systemctl start spinifex.target"
    echo ""
    echo "  5. Verify:"
    echo "     export AWS_PROFILE=spinifex"
    echo "     aws ec2 describe-instance-types"
    echo ""
}

# --- Main ---
main() {
    info "Spinifex installer"
    echo ""

    setup_sudo
    detect_os
    detect_arch
    handle_upgrade
    install_apt_deps
    install_aws_cli
    detect_service_user
    download_spinifex
    install_files
    create_directories
    install_systemd
    install_logrotate
    rm -rf "$SPINIFEX_TMPDIR"
    restart_if_needed
    print_summary
}

main "$@"
