#!/bin/bash

# OVN Compute Node Setup for Hive VPC Networking
#
# This script bootstraps a compute node for OVN-based VPC networking:
#   1. Installs OVN/OVS packages (if not present)
#   2. Enables required services (openvswitch-switch, ovn-controller)
#   3. Creates br-int with secure fail-mode
#   4. Configures OVS external_ids for OVN chassis identity
#   5. Applies sysctl tuning for overlay networking
#
# Usage:
#   ./scripts/setup-ovn.sh [options]
#
# Options:
#   --management     Also start OVN central services (NB DB, SB DB, ovn-northd)
#   --ovn-remote     OVN SB DB address (default: tcp:127.0.0.1:6642)
#   --encap-ip       Geneve tunnel endpoint IP (default: auto-detect)
#   --chassis-id     OVN chassis identifier (default: hostname)
#
# Examples:
#   # Single-node development (management + compute on same host):
#   ./scripts/setup-ovn.sh --management
#
#   # Compute node joining an existing cluster:
#   ./scripts/setup-ovn.sh --ovn-remote=tcp:10.0.0.1:6642 --encap-ip=10.0.0.2
#
#   # Multi-node dev cluster (node1 = management):
#   Node 1: ./scripts/setup-ovn.sh --management --encap-ip=127.0.0.1
#   Node 2: ./scripts/setup-ovn.sh --ovn-remote=tcp:127.0.0.1:6642 --encap-ip=127.0.0.2
#   Node 3: ./scripts/setup-ovn.sh --ovn-remote=tcp:127.0.0.1:6642 --encap-ip=127.0.0.3

set -e

# Defaults
MANAGEMENT=false
OVN_REMOTE="tcp:127.0.0.1:6642"
ENCAP_IP=""
CHASSIS_ID=""

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --management)     MANAGEMENT=true ;;
        --ovn-remote=*)   OVN_REMOTE="${arg#*=}" ;;
        --encap-ip=*)     ENCAP_IP="${arg#*=}" ;;
        --chassis-id=*)   CHASSIS_ID="${arg#*=}" ;;
        --help|-h)
            head -30 "$0" | tail -28
            exit 0
            ;;
        *)
            echo "Unknown option: $arg"
            exit 1
            ;;
    esac
done

# Auto-detect encap IP if not specified
if [ -z "$ENCAP_IP" ]; then
    ENCAP_IP=$(ip -4 route get 8.8.8.8 2>/dev/null | awk '/src/{print $7}' | head -1)
    if [ -z "$ENCAP_IP" ]; then
        ENCAP_IP="127.0.0.1"
    fi
    echo "Auto-detected encap IP: $ENCAP_IP"
fi

# Auto-detect chassis ID if not specified
if [ -z "$CHASSIS_ID" ]; then
    CHASSIS_ID="chassis-$(hostname -s)"
    echo "Auto-detected chassis ID: $CHASSIS_ID"
fi

echo "=== Hive OVN Compute Node Setup ==="
echo "  Management node: $MANAGEMENT"
echo "  OVN Remote (SB): $OVN_REMOTE"
echo "  Encap IP:        $ENCAP_IP"
echo "  Chassis ID:      $CHASSIS_ID"
echo ""

# --- Step 1: Install packages ---
echo "Step 1: Checking OVN/OVS packages..."

install_packages() {
    local missing=()
    for pkg in openvswitch-switch ovn-host; do
        if ! dpkg -s "$pkg" >/dev/null 2>&1; then
            missing+=("$pkg")
        fi
    done
    if [ "$MANAGEMENT" = true ]; then
        if ! dpkg -s ovn-central >/dev/null 2>&1; then
            missing+=("ovn-central")
        fi
    fi

    if [ ${#missing[@]} -gt 0 ]; then
        echo "  Installing: ${missing[*]}"
        sudo apt-get update -qq
        sudo apt-get install -y -qq "${missing[@]}"
    else
        echo "  All packages installed"
    fi
}

install_packages

# --- Step 2: Enable services ---
echo ""
echo "Step 2: Enabling services..."

sudo systemctl enable --now openvswitch-switch
echo "  openvswitch-switch: enabled"

if [ "$MANAGEMENT" = true ]; then
    sudo systemctl enable --now ovn-central
    echo "  ovn-central: enabled (NB DB + SB DB + ovn-northd)"

    # Allow remote connections to NB and SB databases
    sudo ovn-nbctl set-connection ptcp:6641
    sudo ovn-sbctl set-connection ptcp:6642
    echo "  OVN NB DB listening on tcp:6641"
    echo "  OVN SB DB listening on tcp:6642"
fi

# --- Step 3: Create and configure br-int ---
echo ""
echo "Step 3: Configuring br-int..."

sudo ovs-vsctl --may-exist add-br br-int
sudo ovs-vsctl set Bridge br-int fail-mode=secure
sudo ovs-vsctl set Bridge br-int other-config:disable-in-band=true
sudo ip link set br-int up
echo "  br-int: created, fail-mode=secure, up"

# --- Step 4: Configure OVN external_ids ---
echo ""
echo "Step 4: Setting OVS external_ids for OVN..."

sudo ovs-vsctl set Open_vSwitch . \
    external_ids:system-id="$CHASSIS_ID" \
    external_ids:ovn-remote="$OVN_REMOTE" \
    external_ids:ovn-encap-ip="$ENCAP_IP" \
    external_ids:ovn-encap-type="geneve"

echo "  system-id:      $CHASSIS_ID"
echo "  ovn-remote:     $OVN_REMOTE"
echo "  ovn-encap-ip:   $ENCAP_IP"
echo "  ovn-encap-type: geneve"

# --- Step 5: Start ovn-controller ---
echo ""
echo "Step 5: Starting ovn-controller..."

sudo systemctl enable --now ovn-controller
echo "  ovn-controller: enabled and started"

# --- Step 6: Sysctl tuning ---
echo ""
echo "Step 6: Applying sysctl for overlay networking..."

sudo tee /etc/sysctl.d/99-hive-vpc.conf >/dev/null <<'SYSCTL'
# Hive VPC networking: enable IP forwarding and disable rp_filter
# for Geneve overlay traffic on OVS bridges.
net.ipv4.ip_forward=1
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
SYSCTL
sudo sysctl --system -q
echo "  ip_forward=1, rp_filter=0"

# --- Step 7: Verify Geneve kernel support ---
echo ""
echo "Step 7: Verifying Geneve kernel module..."

if sudo modprobe geneve 2>/dev/null; then
    echo "  geneve module: loaded"
else
    echo "  WARNING: geneve module not available (tunnels may not work)"
fi

# --- Step 8: Grant non-root access to OVS/OVN ---
echo ""
echo "Step 8: Configuring non-root access..."

# Open OVS DB socket so non-root processes can use ovs-vsctl
OVS_SOCK="/var/run/openvswitch/db.sock"
if [ -S "$OVS_SOCK" ]; then
    sudo chmod 0666 "$OVS_SOCK"
    echo "  OVS DB socket: opened ($OVS_SOCK)"
fi

# Open OVN runtime directory and ctl sockets for ovs-appctl access
if [ -d "/var/run/ovn" ]; then
    sudo chmod 0755 /var/run/ovn
    sudo chmod 0666 /var/run/ovn/*.ctl 2>/dev/null || true
    echo "  OVN ctl sockets: opened (/var/run/ovn/)"
fi
if [ -d "/var/run/openvswitch" ]; then
    sudo chmod 0666 /var/run/openvswitch/*.ctl 2>/dev/null || true
fi

# Create persistent systemd override so permissions survive OVS restarts
OVERRIDE_DIR="/etc/systemd/system/openvswitch-switch.service.d"
if [ ! -f "$OVERRIDE_DIR/hive-perms.conf" ]; then
    sudo mkdir -p "$OVERRIDE_DIR"
    sudo tee "$OVERRIDE_DIR/hive-perms.conf" >/dev/null <<'OVERRIDE'
[Service]
ExecStartPost=/bin/chmod 0666 /var/run/openvswitch/db.sock
OVERRIDE
    sudo systemctl daemon-reload
    echo "  systemd override: created (db.sock permissions persist across restarts)"
else
    echo "  systemd override: already exists"
fi

# Create sudoers rule for network commands that always need root
# (ip tuntap, ip link set — NET_ADMIN operations)
SUDOERS_FILE="/etc/sudoers.d/hive-network"
if [ ! -f "$SUDOERS_FILE" ]; then
    CURRENT_USER=$(whoami)
    sudo tee "$SUDOERS_FILE" >/dev/null <<EOF
# Hive VPC networking: allow non-root daemon to manage tap devices and OVS
$CURRENT_USER ALL=(root) NOPASSWD: /sbin/ip, /usr/sbin/ip
$CURRENT_USER ALL=(root) NOPASSWD: /usr/bin/ovs-vsctl, /usr/bin/ovs-appctl
$CURRENT_USER ALL=(root) NOPASSWD: /usr/bin/ovn-nbctl, /usr/bin/ovn-sbctl
EOF
    sudo chmod 0440 "$SUDOERS_FILE"
    echo "  sudoers rule: created ($SUDOERS_FILE)"
else
    echo "  sudoers rule: already exists"
fi

# --- Step 9: Health check ---
echo ""
echo "Step 9: Verifying setup..."

OK=true

# Check br-int
if sudo ovs-vsctl br-exists br-int; then
    echo "  br-int:          OK"
else
    echo "  br-int:          FAILED"
    OK=false
fi

# Check ovn-controller
if sudo ovs-appctl -t ovn-controller version >/dev/null 2>&1; then
    echo "  ovn-controller:  OK"
else
    echo "  ovn-controller:  FAILED (may still be starting)"
    OK=false
fi

# Check chassis registration (may take a moment)
if [ "$MANAGEMENT" = true ]; then
    sleep 2
    CHASSIS_COUNT=$(sudo ovn-sbctl show 2>/dev/null | grep -c "Chassis" || true)
    echo "  chassis count:   $CHASSIS_COUNT"
fi

echo ""
if [ "$OK" = true ]; then
    echo "=== OVN compute node setup complete ==="
else
    echo "=== Setup completed with warnings (check above) ==="
fi
