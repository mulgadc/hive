#!/bin/bash
# Hive binary installer
# Usage: curl -sfL https://install.mulgadc.com/setup.sh | bash
#
# Environment variables:
#   INSTALL_HIVE_CHANNEL   Release channel: latest (default), dev
#   INSTALL_HIVE_VERSION   Pin to specific version (overrides channel)
#   INSTALL_HIVE_TARBALL   Path to local tarball (skips download, for testing/air-gapped)
#   INSTALL_HIVE_SKIP_APT  Set to 1 to skip apt dependency install
#   INSTALL_HIVE_SKIP_AWS  Set to 1 to skip AWS CLI install

set -e

INSTALL_HIVE_CHANNEL="${INSTALL_HIVE_CHANNEL:-latest}"
INSTALL_BASE_URL="${INSTALL_BASE_URL:-https://install.mulgadc.com}"

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fatal() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# --- Root check ---
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        fatal "This script must be run as root. Use: curl -sfL ... | sudo bash"
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
            fatal "Unsupported OS: $ID $VERSION_ID. Hive requires Debian 12+ or Ubuntu 22.04+"
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
            fatal "Unsupported architecture: $MACHINE. Hive requires x86_64 or aarch64"
            ;;
    esac

    info "Detected architecture: $MACHINE ($ARCH)"
}

# --- Detect service user ---
detect_service_user() {
    # Use the real user who invoked sudo (not root)
    HIVE_USER="${SUDO_USER:-$(whoami)}"
    HIVE_GROUP="$(id -gn "$HIVE_USER")"

    if [ "$HIVE_USER" = "root" ]; then
        fatal "Cannot determine service user. Do not run as root directly — use: sudo bash setup.sh"
    fi

    info "Services will run as: $HIVE_USER:$HIVE_GROUP"

    # Allow the service user to manage OVN/OVS and tap devices
    cat > /etc/sudoers.d/hive-network << SUDOERS
# Hive VPC networking: allow service user to manage tap devices, OVS, and OVN
${HIVE_USER} ALL=(root) NOPASSWD: /sbin/ip, /usr/sbin/ip
${HIVE_USER} ALL=(root) NOPASSWD: /usr/bin/ovs-vsctl, /usr/bin/ovs-appctl
${HIVE_USER} ALL=(root) NOPASSWD: /usr/bin/ovn-nbctl, /usr/bin/ovn-sbctl
SUDOERS
    chmod 0440 /etc/sudoers.d/hive-network
    info "Sudoers rules installed for $HIVE_USER"
}

# --- Install apt dependencies ---
install_apt_deps() {
    if [ "${INSTALL_HIVE_SKIP_APT}" = "1" ]; then
        info "Skipping apt dependencies (INSTALL_HIVE_SKIP_APT=1)"
        return
    fi

    info "Installing system dependencies..."
    apt-get update -qq

    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
        nbdkit \
        $QEMU_PACKAGES qemu-utils qemu-kvm \
        libvirt-daemon-system libvirt-clients \
        jq curl iproute2 netcat-openbsd wget unzip xz-utils file \
        ovn-central ovn-host openvswitch-switch \
        > /dev/null

    info "System dependencies installed"
}

# --- Install AWS CLI ---
install_aws_cli() {
    if [ "${INSTALL_HIVE_SKIP_AWS}" = "1" ]; then
        info "Skipping AWS CLI (INSTALL_HIVE_SKIP_AWS=1)"
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
    "$AWS_TMPDIR/aws/install" --update > /dev/null
    rm -rf "$AWS_TMPDIR"

    info "AWS CLI installed: $(aws --version 2>&1 | head -1)"
}

# --- Download tarball ---
download_hive() {
    HIVE_TMPDIR=$(mktemp -d)
    TARBALL="$HIVE_TMPDIR/hive.tar.gz"

    # Local tarball override — skip download (for testing and air-gapped installs)
    if [ -n "$INSTALL_HIVE_TARBALL" ]; then
        info "Using local tarball: $INSTALL_HIVE_TARBALL"
        cp "$INSTALL_HIVE_TARBALL" "$TARBALL"
        info "Extracting..."
        tar -xzf "$TARBALL" -C "$HIVE_TMPDIR"
        EXTRACT_DIR="$HIVE_TMPDIR"
        return
    fi

    if [ -n "$INSTALL_HIVE_VERSION" ]; then
        DOWNLOAD_URL="${INSTALL_BASE_URL}/download/${INSTALL_HIVE_VERSION}/${ARCH}"
        info "Downloading Hive $INSTALL_HIVE_VERSION ($ARCH)..."
    else
        DOWNLOAD_URL="${INSTALL_BASE_URL}/download/${INSTALL_HIVE_CHANNEL}/${ARCH}"
        info "Downloading Hive ($INSTALL_HIVE_CHANNEL channel, $ARCH)..."
    fi

    HTTP_CODE=$(curl -fsSL -w '%{http_code}' -o "$TARBALL" "$DOWNLOAD_URL" 2>/dev/null) || true
    if [ ! -f "$TARBALL" ] || [ "$HTTP_CODE" -ge 400 ] 2>/dev/null; then
        rm -rf "$HIVE_TMPDIR"
        fatal "Failed to download Hive from $DOWNLOAD_URL (HTTP $HTTP_CODE)"
    fi

    # Verify checksum if available
    CHECKSUM_URL="${DOWNLOAD_URL}.sha256"
    if curl -fsSL -o "$HIVE_TMPDIR/checksum.sha256" "$CHECKSUM_URL" 2>/dev/null; then
        info "Verifying checksum..."
        EXPECTED=$(awk '{print $1}' "$HIVE_TMPDIR/checksum.sha256")
        ACTUAL=$(sha256sum "$TARBALL" | awk '{print $1}')
        if [ "$EXPECTED" != "$ACTUAL" ]; then
            rm -rf "$HIVE_TMPDIR"
            fatal "Checksum verification failed. Expected: $EXPECTED, Got: $ACTUAL"
        fi
        info "Checksum verified"
    else
        warn "Checksum not available, skipping verification"
    fi

    # Extract
    info "Extracting..."
    tar -xzf "$TARBALL" -C "$HIVE_TMPDIR"
    EXTRACT_DIR="$HIVE_TMPDIR"
}

# --- Place files ---
install_files() {
    info "Installing files..."

    # Binary
    install -m 0755 "$EXTRACT_DIR/hive" /usr/local/bin/hive
    info "  /usr/local/bin/hive"

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
    mkdir -p "$PLUGINDIR"
    install -m 0755 "$EXTRACT_DIR/nbdkit-viperblock-plugin.so" "$PLUGINDIR/nbdkit-viperblock-plugin.so"
    info "  $PLUGINDIR/nbdkit-viperblock-plugin.so"

    # Setup scripts
    mkdir -p /usr/local/share/hive
    if [ -f "$EXTRACT_DIR/setup-ovn.sh" ]; then
        install -m 0755 "$EXTRACT_DIR/setup-ovn.sh" /usr/local/share/hive/setup-ovn.sh
        info "  /usr/local/share/hive/setup-ovn.sh"
    fi

    # Cleanup temp dir after all install steps complete (defer to main)
}

# --- Create directories ---
create_directories() {
    info "Creating directories..."

    mkdir -p /etc/hive
    chmod 0700 /etc/hive
    chown "$HIVE_USER:$HIVE_GROUP" /etc/hive

    mkdir -p /var/lib/hive
    chown "$HIVE_USER:$HIVE_GROUP" /var/lib/hive

    # Symlink so services that expect BaseDir/config/ can find /etc/hive/
    if [ ! -e /var/lib/hive/config ]; then
        ln -s /etc/hive /var/lib/hive/config
    fi

    # Symlink so services that write logs to BaseDir/logs/ use /var/log/hive/
    if [ ! -e /var/lib/hive/logs ]; then
        ln -s /var/log/hive /var/lib/hive/logs
    fi

    mkdir -p /var/log/hive
    chown "$HIVE_USER:$HIVE_GROUP" /var/log/hive

    mkdir -p /run/hive
    chown "$HIVE_USER:$HIVE_GROUP" /run/hive

    mkdir -p /var/lib/hive/viperblock

    # Generate environment file with install-specific values (e.g. arch-dependent paths)
    cat > /etc/hive/systemd.env << EOF
# Generated by setup.sh — install-specific environment variables
HIVE_VIPERBLOCK_PLUGIN_PATH=${PLUGINDIR}/nbdkit-viperblock-plugin.so
EOF
    chown "$HIVE_USER:$HIVE_GROUP" /etc/hive/systemd.env
    info "Generated /etc/hive/systemd.env"
}

# --- Install systemd units ---
install_systemd() {
    info "Installing systemd units..."

    if [ ! -d "$EXTRACT_DIR/systemd" ]; then
        fatal "Systemd unit files not found in tarball (expected systemd/ directory)"
    fi

    for unit in "$EXTRACT_DIR"/systemd/*; do
        # Substitute User= and Group= with the detected service user
        sed "s/^User=hive$/User=$HIVE_USER/;s/^Group=hive$/Group=$HIVE_GROUP/" \
            "$unit" > "/etc/systemd/system/$(basename "$unit")"
        chmod 0644 "/etc/systemd/system/$(basename "$unit")"
        info "  /etc/systemd/system/$(basename "$unit")"
    done

    systemctl daemon-reload
    info "Systemd units installed (running as $HIVE_USER:$HIVE_GROUP)"
}

# --- Install logrotate ---
install_logrotate() {
    if [ -f "$EXTRACT_DIR/logrotate-hive" ]; then
        install -m 0644 "$EXTRACT_DIR/logrotate-hive" /etc/logrotate.d/hive
    else
        warn "Logrotate config not found in tarball, skipping"
        return
    fi
    info "Logrotate config installed"
}

# --- Upgrade handling ---
handle_upgrade() {
    if systemctl is-active --quiet hive.target 2>/dev/null; then
        warn "Hive services are running. Stopping for upgrade..."
        systemctl stop hive.target
        RESTART_AFTER=true
    fi
}

restart_if_needed() {
    if [ "${RESTART_AFTER}" = "true" ]; then
        info "Restarting Hive services..."
        systemctl start hive.target
    fi
}

# --- Print summary ---
print_summary() {
    INSTALLED_VERSION=$(/usr/local/bin/hive version 2>/dev/null || echo "unknown")

    echo ""
    echo "============================================"
    echo "  Hive installed successfully"
    echo "============================================"
    echo ""
    echo "  Version:      $INSTALLED_VERSION"
    echo "  Architecture: $ARCH"
    echo "  Binary:       /usr/local/bin/hive"
    echo "  Config:       /etc/hive/"
    echo "  Data:         /var/lib/hive/"
    echo "  Logs:         /var/log/hive/"
    echo ""
    echo "  Next steps:"
    echo ""
    echo "  1. Setup OVN networking:"
    echo "     sudo /usr/local/share/hive/setup-ovn.sh --management"
    echo ""
    echo "  2. Initialize a region:"
    echo "     sudo hive admin init --region <region> --az <az> --node node1 --nodes 1"
    echo ""
    echo "  3. Trust the CA certificate:"
    echo "     sudo cp /etc/hive/ca.pem /usr/local/share/ca-certificates/hive-ca.crt"
    echo "     sudo update-ca-certificates"
    echo ""
    echo "  4. Start services:"
    echo "     sudo systemctl start hive.target"
    echo ""
    echo "  5. Verify:"
    echo "     export AWS_PROFILE=hive"
    echo "     aws ec2 describe-instance-types"
    echo ""
}

# --- Main ---
main() {
    info "Hive installer"
    echo ""

    check_root
    detect_os
    detect_arch
    handle_upgrade
    install_apt_deps
    install_aws_cli
    detect_service_user
    download_hive
    install_files
    create_directories
    install_systemd
    install_logrotate
    rm -rf "$HIVE_TMPDIR"
    restart_if_needed
    print_summary
}

main "$@"
