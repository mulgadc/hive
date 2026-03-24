#!/bin/bash

# OVN Compute Node Setup for Spinifex VPC Networking
#
# This script bootstraps a compute node for OVN-based VPC networking:
#   1. Installs OVN/OVS packages (if not present)
#   2. Enables required services (openvswitch-switch, ovn-controller)
#   3. Creates br-int with secure fail-mode
#   4. Configures WAN bridge for public subnet uplink (auto-detected or manual)
#   5. Configures OVS external_ids for OVN chassis identity
#   6. Applies sysctl tuning for overlay networking
#
# Usage:
#   ./scripts/setup-ovn.sh [options]
#
# Options:
#   --management         Also start OVN central services (NB DB, SB DB, ovn-northd)
#   --wan-bridge=NAME    OVS bridge for WAN traffic (default: auto-detect from default route)
#   --wan-iface=NAME     Physical NIC to add to the WAN bridge (use with --wan-bridge or --macvlan)
#   --macvlan            Create macvlan off --wan-iface instead of moving NIC directly.
#                        SSH-safe for single-NIC hosts where WAN NIC carries SSH.
#   --dhcp               Obtain gateway IP via DHCP on the WAN bridge interface
#   --ovn-remote=ADDR    OVN SB DB address (default: tcp:127.0.0.1:6642)
#   --encap-ip=IP        Geneve tunnel endpoint IP (default: auto-detect)
#   --chassis-id=ID      OVN chassis identifier (default: chassis-$(hostname -s))
#
# WAN Bridge Auto-Detection:
#   When no --wan-bridge is given, the script checks the default route interface:
#   - If it's already a bridge (e.g. br-wan from cloud-init) → use it directly
#   - If it's a physical NIC → stop and print guidance (cannot safely move NIC)
#
# Examples:
#   # WAN is already on a bridge (tofu-cluster, production):
#   ./scripts/setup-ovn.sh --management
#
#   # Dedicated WAN NIC (not your SSH NIC — you take responsibility):
#   ./scripts/setup-ovn.sh --management --wan-bridge=br-wan --wan-iface=eth1
#
#   # Single-NIC host (SSH-safe macvlan):
#   ./scripts/setup-ovn.sh --management --macvlan --wan-iface=eth0
#
#   # Compute node joining an existing cluster:
#   ./scripts/setup-ovn.sh --ovn-remote=tcp:10.0.0.1:6642 --encap-ip=10.0.0.2
#
#   # No WAN bridge (overlay-only, no public subnet):
#   ./scripts/setup-ovn.sh --management --encap-ip=10.0.0.1

set -e

# Defaults
MANAGEMENT=false
WAN_BRIDGE=""
WAN_IFACE=""
MACVLAN_MODE=false
EXTERNAL_DHCP=false
OVN_REMOTE="tcp:127.0.0.1:6642"
ENCAP_IP=""
CHASSIS_ID=""

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --management)       MANAGEMENT=true ;;
        --macvlan)          MACVLAN_MODE=true ;;
        --dhcp)             EXTERNAL_DHCP=true ;;
        --wan-bridge=*)     WAN_BRIDGE="${arg#*=}" ;;
        --wan-iface=*)      WAN_IFACE="${arg#*=}" ;;
        --ovn-remote=*)     OVN_REMOTE="${arg#*=}" ;;
        --encap-ip=*)       ENCAP_IP="${arg#*=}" ;;
        --chassis-id=*)     CHASSIS_ID="${arg#*=}" ;;
        --help|-h)
            head -44 "$0" | tail -42
            exit 0
            ;;
        *)
            echo "Unknown option: $arg"
            exit 1
            ;;
    esac
done

# --- WAN bridge auto-detection ---
# Determine the WAN bridge name and how to set it up.
WAN_BRIDGE_MODE=""  # "existing", "direct", "macvlan", or ""

detect_wan_bridge() {
    # If --wan-bridge was explicitly given, use it
    if [ -n "$WAN_BRIDGE" ]; then
        if [ "$MACVLAN_MODE" = true ] && [ -n "$WAN_IFACE" ]; then
            WAN_BRIDGE_MODE="macvlan"
        elif [ -n "$WAN_IFACE" ]; then
            WAN_BRIDGE_MODE="direct"
        elif sudo ovs-vsctl br-exists "$WAN_BRIDGE" 2>/dev/null; then
            WAN_BRIDGE_MODE="existing"
        else
            # Bridge doesn't exist yet and no --wan-iface — create empty OVS bridge
            WAN_BRIDGE_MODE="existing"
        fi
        return
    fi

    # If --macvlan was given without --wan-bridge, we need --wan-iface
    if [ "$MACVLAN_MODE" = true ]; then
        if [ -z "$WAN_IFACE" ]; then
            echo "ERROR: --macvlan requires --wan-iface=<NIC>"
            exit 1
        fi
        WAN_BRIDGE="br-wan"
        WAN_BRIDGE_MODE="macvlan"
        return
    fi

    # Auto-detect: find the default route interface
    local default_dev
    default_dev=$(ip -4 route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}' | head -1)

    if [ -z "$default_dev" ]; then
        echo "  No default route found — no WAN bridge configured"
        echo "  (VMs will not have external connectivity)"
        return
    fi

    # Check if the default route device is a bridge (Linux or OVS)
    local is_bridge=false
    if ip -d link show "$default_dev" 2>/dev/null | grep -q "bridge"; then
        is_bridge=true
    fi
    if sudo ovs-vsctl br-exists "$default_dev" 2>/dev/null; then
        is_bridge=true
    fi

    if [ "$is_bridge" = true ]; then
        # Default route goes through a bridge — use it directly
        WAN_BRIDGE="$default_dev"
        WAN_BRIDGE_MODE="existing"
        echo "  Auto-detected WAN bridge: $WAN_BRIDGE (default route interface)"
        return
    fi

    # Default route is a physical NIC — cannot safely move it to OVS
    # because it might be carrying SSH.
    local wan_ip
    wan_ip=$(ip -4 -o addr show "$default_dev" 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)

    echo ""
    echo "============================================================"
    echo "  WAN interface '$default_dev' ($wan_ip) is a physical NIC."
    echo "  Cannot auto-create a bridge — this may drop your connection."
    echo ""
    echo "  Options:"
    echo ""
    echo "  1. Create a WAN bridge first (e.g. via netplan), then re-run:"
    echo "     ./scripts/setup-ovn.sh --management"
    echo ""
    echo "  2. Dedicated WAN NIC (NOT your SSH connection):"
    echo "     ./scripts/setup-ovn.sh --management --wan-bridge=br-wan --wan-iface=$default_dev"
    echo ""
    echo "  3. Single-NIC host (SSH-safe macvlan):"
    echo "     ./scripts/setup-ovn.sh --management --macvlan --wan-iface=$default_dev"
    echo ""
    echo "  4. No external networking (overlay-only):"
    echo "     ./scripts/setup-ovn.sh --management --encap-ip=$wan_ip"
    echo "============================================================"
    echo ""
    exit 1
}

detect_wan_bridge

# Auto-detect encap IP if not specified
if [ -z "$ENCAP_IP" ]; then
    # Prefer br-vpc IP if it exists (dedicated VPC data plane)
    if ip -4 addr show br-vpc >/dev/null 2>&1; then
        ENCAP_IP=$(ip -4 -o addr show br-vpc 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)
        if [ -n "$ENCAP_IP" ]; then
            echo "Auto-detected encap IP from br-vpc: $ENCAP_IP"
        fi
    fi
    # Fall back to default route source IP
    if [ -z "$ENCAP_IP" ]; then
        ENCAP_IP=$(ip -4 route get 8.8.8.8 2>/dev/null | awk '/src/{print $7}' | head -1)
        if [ -z "$ENCAP_IP" ]; then
            ENCAP_IP="127.0.0.1"
        fi
        echo "Auto-detected encap IP: $ENCAP_IP"
    fi
fi

# Auto-detect chassis ID if not specified
if [ -z "$CHASSIS_ID" ]; then
    CHASSIS_ID="chassis-$(hostname -s)"
    echo "Auto-detected chassis ID: $CHASSIS_ID"
fi

echo "=== Spinifex OVN Compute Node Setup ==="
echo "  Management node:  $MANAGEMENT"
if [ -n "$WAN_BRIDGE" ]; then
    echo "  WAN bridge:       $WAN_BRIDGE ($WAN_BRIDGE_MODE)"
    if [ -n "$WAN_IFACE" ]; then
        echo "  WAN interface:    $WAN_IFACE"
    fi
else
    echo "  WAN bridge:       none (overlay-only)"
fi
echo "  OVN Remote (SB):  $OVN_REMOTE"
echo "  Encap IP:         $ENCAP_IP"
echo "  Chassis ID:       $CHASSIS_ID"
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

sudo systemctl enable openvswitch-switch
sudo systemctl start openvswitch-switch
echo "  openvswitch-switch: started"

if [ "$MANAGEMENT" = true ]; then
    sudo systemctl enable ovn-central
    sudo systemctl start ovn-central
    echo "  ovn-central: started (NB DB + SB DB + ovn-northd)"

    # Wait for OVN NB DB socket to become available
    for i in $(seq 1 15); do
        if sudo ovn-nbctl --timeout=2 get-connection >/dev/null 2>&1; then
            break
        fi
        echo "  Waiting for OVN NB DB... ($i/15)"
        sleep 1
    done

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

# --- Step 3b: Configure WAN bridge for public subnet uplink ---
if [ -n "$WAN_BRIDGE" ]; then
    echo ""
    echo "Step 3b: Configuring WAN bridge ($WAN_BRIDGE) for public subnet uplink..."

    case "$WAN_BRIDGE_MODE" in
        existing)
            # Bridge already exists (e.g. br-wan from cloud-init).
            # Ensure it's registered as an OVS bridge. If it's a Linux bridge
            # that OVS doesn't know about, create a new OVS bridge with the
            # same name — OVS will take over.
            if ! sudo ovs-vsctl br-exists "$WAN_BRIDGE" 2>/dev/null; then
                sudo ovs-vsctl --may-exist add-br "$WAN_BRIDGE"
                echo "  created OVS bridge: $WAN_BRIDGE"
            fi
            sudo ip link set "$WAN_BRIDGE" up
            echo "  $WAN_BRIDGE: existing bridge, up"
            ;;

        direct)
            # Add WAN NIC directly to OVS bridge. The NIC becomes an OVS slave —
            # its IP (if any) is no longer reachable from the host. The user has
            # confirmed this NIC is NOT their SSH connection.
            if ! ip link show "$WAN_IFACE" >/dev/null 2>&1; then
                echo "  ERROR: interface $WAN_IFACE does not exist"
                echo "  Available interfaces:"
                ip -o link show | awk -F': ' '{print "    " $2}'
                exit 1
            fi

            sudo ovs-vsctl --may-exist add-br "$WAN_BRIDGE"
            sudo ip link set "$WAN_BRIDGE" up

            if sudo ovs-vsctl port-to-br "$WAN_IFACE" >/dev/null 2>&1; then
                echo "  $WAN_IFACE already on $(sudo ovs-vsctl port-to-br "$WAN_IFACE")"
            else
                sudo ovs-vsctl --may-exist add-port "$WAN_BRIDGE" "$WAN_IFACE"
                echo "  added $WAN_IFACE directly to $WAN_BRIDGE"
            fi
            sudo ip link set "$WAN_IFACE" up
            echo "  $WAN_BRIDGE: direct bridge on $WAN_IFACE"
            echo "  NOTE: $WAN_IFACE is now an OVS port — no host IP on this NIC"
            ;;

        macvlan)
            # Create a macvlan sub-interface in bridge mode off the WAN NIC.
            # The host keeps its IP on the parent NIC — SSH-safe. OVN localnet
            # traffic flows through the macvlan to the physical wire.
            if ! ip link show "$WAN_IFACE" >/dev/null 2>&1; then
                echo "  ERROR: interface $WAN_IFACE does not exist"
                echo "  Available interfaces:"
                ip -o link show | awk -F': ' '{print "    " $2}'
                exit 1
            fi

            MACVLAN_NAME="spx-ext-${WAN_IFACE}"

            sudo ovs-vsctl --may-exist add-br "$WAN_BRIDGE"
            sudo ip link set "$WAN_BRIDGE" up

            if ip link show "$MACVLAN_NAME" >/dev/null 2>&1; then
                echo "  macvlan $MACVLAN_NAME already exists"
            else
                sudo ip link add "$MACVLAN_NAME" link "$WAN_IFACE" type macvlan mode bridge
                echo "  created macvlan: $MACVLAN_NAME (bridge mode) on $WAN_IFACE"
            fi

            sudo ip link set "$MACVLAN_NAME" up
            sudo ovs-vsctl --may-exist add-port "$WAN_BRIDGE" "$MACVLAN_NAME"
            echo "  $WAN_BRIDGE: macvlan port $MACVLAN_NAME on $WAN_IFACE"
            echo "  NOTE: host keeps its IP on $WAN_IFACE (SSH-safe)"
            echo "  QUIRK: host cannot reach VMs at their public IPs (macvlan isolation)"
            ;;
    esac

    # --- DHCP: obtain gateway IP for OVN SNAT ---
    if [ "$EXTERNAL_DHCP" = true ]; then
        echo ""
        echo "Step 3c: Obtaining external gateway IP via DHCP..."

        # For macvlan mode, DHCP on the macvlan interface (it has L2 access to WAN).
        # For direct/existing bridge, DHCP on the bridge itself.
        if [ "$WAN_BRIDGE_MODE" = "macvlan" ]; then
            DHCP_IFACE="spx-ext-${WAN_IFACE}"
        else
            DHCP_IFACE="$WAN_BRIDGE"
        fi

        # Run DHCP client to get a lease
        if command -v dhcpcd >/dev/null 2>&1; then
            sudo dhcpcd --waitip=4 --timeout 15 "$DHCP_IFACE" 2>/dev/null || true
        elif command -v dhclient >/dev/null 2>&1; then
            sudo dhclient -1 -timeout 15 "$DHCP_IFACE" 2>/dev/null || true
        else
            echo "  WARNING: no DHCP client found (dhcpcd or dhclient)"
            echo "  Install dhcpcd-base or isc-dhcp-client, or set gateway_ip manually"
        fi

        # Read the obtained IP
        DHCP_IP=$(ip -4 addr show dev "$DHCP_IFACE" 2>/dev/null | awk '/inet /{print $2}' | head -1 | cut -d/ -f1)
        if [ -n "$DHCP_IP" ]; then
            echo "  DHCP obtained: $DHCP_IP on $DHCP_IFACE"

            # Write the gateway IP to the spinifex config so vpcd can use it
            CONFIG_DIR="${CONFIG_DIR:-$HOME/spinifex/config}"
            CONFIG_FILE="$CONFIG_DIR/spinifex.toml"
            if [ -f "$CONFIG_FILE" ]; then
                if grep -q "gateway_ip" "$CONFIG_FILE"; then
                    sed -i "s/gateway_ip.*/gateway_ip = \"$DHCP_IP\"/" "$CONFIG_FILE"
                else
                    sed -i "/^gateway *=.*/a gateway_ip  = \"$DHCP_IP\"" "$CONFIG_FILE"
                fi
                echo "  Updated $CONFIG_FILE with gateway_ip = $DHCP_IP"
            else
                echo "  WARNING: $CONFIG_FILE not found — set gateway_ip manually"
            fi
        else
            echo "  WARNING: DHCP failed to obtain IP on $DHCP_IFACE"
            echo "  VMs will not have external connectivity until gateway_ip is configured"
        fi
    fi
fi

# --- Step 4: Configure OVN external_ids ---
echo ""
echo "Step 4: Setting OVS external_ids for OVN..."

if [ -n "$WAN_BRIDGE" ]; then
    BRIDGE_MAPPINGS="external:${WAN_BRIDGE}"
    sudo ovs-vsctl set Open_vSwitch . \
        external_ids:system-id="$CHASSIS_ID" \
        external_ids:ovn-remote="$OVN_REMOTE" \
        external_ids:ovn-encap-ip="$ENCAP_IP" \
        external_ids:ovn-encap-type="geneve" \
        external_ids:ovn-bridge-mappings="$BRIDGE_MAPPINGS"
    echo "  ovn-bridge-mappings: $BRIDGE_MAPPINGS"
else
    sudo ovs-vsctl set Open_vSwitch . \
        external_ids:system-id="$CHASSIS_ID" \
        external_ids:ovn-remote="$OVN_REMOTE" \
        external_ids:ovn-encap-ip="$ENCAP_IP" \
        external_ids:ovn-encap-type="geneve"
fi

echo "  system-id:      $CHASSIS_ID"
echo "  ovn-remote:     $OVN_REMOTE"
echo "  ovn-encap-ip:   $ENCAP_IP"
echo "  ovn-encap-type: geneve"

# --- Step 5: Start ovn-controller ---
echo ""
echo "Step 5: Starting ovn-controller..."

sudo systemctl start ovn-controller
echo "  ovn-controller: started"

# --- Step 6: Sysctl tuning ---
echo ""
echo "Step 6: Applying sysctl for overlay networking..."

sudo tee /etc/sysctl.d/99-spinifex-vpc.conf >/dev/null <<'SYSCTL'
# Spinifex VPC networking: enable IP forwarding and disable rp_filter
# for Geneve overlay traffic on OVS bridges.
net.ipv4.ip_forward=1
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
SYSCTL
sudo sysctl --system -q
echo "  ip_forward=1, rp_filter=0"

# --- Step 6b: Ensure data NIC routing for Geneve tunnels ---
echo ""
echo "Step 6b: Configuring data NIC routing for Geneve tunnels..."

# When management and data NICs share the same subnet (e.g. both on 10.1.0.0/16),
# the kernel may route Geneve tunnel traffic through the management NIC with the
# wrong source IP. This causes remote OVS nodes to drop incoming tunnel packets
# because the source IP doesn't match the configured tunnel remote_ip.
# Fix: lower the route metric on the data NIC so it's preferred.
DATA_IFACE=$(ip -o -4 addr show | awk -v ip="$ENCAP_IP" '$0 ~ ip"/" {print $2}')
if [ -n "$DATA_IFACE" ]; then
    SUBNET=$(ip -o -4 route show dev "$DATA_IFACE" proto kernel scope link | awk '{print $1}' | head -1)
    if [ -n "$SUBNET" ]; then
        sudo ip route replace "$SUBNET" dev "$DATA_IFACE" src "$ENCAP_IP" metric 50
        echo "  data route: $SUBNET via $DATA_IFACE src $ENCAP_IP (metric 50)"
    else
        echo "  skipped: no kernel route found for $DATA_IFACE"
    fi
else
    echo "  skipped: could not find interface for $ENCAP_IP"
fi

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
if [ ! -f "$OVERRIDE_DIR/spinifex-perms.conf" ]; then
    sudo mkdir -p "$OVERRIDE_DIR"
    sudo tee "$OVERRIDE_DIR/spinifex-perms.conf" >/dev/null <<'OVERRIDE'
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
SUDOERS_FILE="/etc/sudoers.d/spinifex-network"
if [ ! -f "$SUDOERS_FILE" ]; then
    CURRENT_USER=$(whoami)
    sudo tee "$SUDOERS_FILE" >/dev/null <<EOF
# Spinifex VPC networking: allow non-root daemon to manage tap devices and OVS
$CURRENT_USER ALL=(root) NOPASSWD: /sbin/ip, /usr/sbin/ip
$CURRENT_USER ALL=(root) NOPASSWD: /usr/bin/ovs-vsctl, /usr/bin/ovs-appctl
$CURRENT_USER ALL=(root) NOPASSWD: /usr/bin/ovn-nbctl, /usr/bin/ovn-sbctl
EOF
    sudo chmod 0440 "$SUDOERS_FILE"
    echo "  sudoers rule: created ($SUDOERS_FILE)"
else
    echo "  sudoers rule: already exists"
fi

# --- Step 9: Configure OVN log rotation ---
# OVN has built-in rotation that renames foo.log → foo.log.log. A previous
# logrotate config used a *.log glob that caught those .log.log files too,
# creating .log.log.1 files that accumulated to 27GB. Fix: use explicit
# filenames so logrotate only touches the primary logs, not OVN's backups.
echo ""
echo "Step 9: Configuring OVN log rotation..."

# Clean up stale .log.log files from the old double-rotation bug
if ls /var/log/ovn/*.log.log* 1>/dev/null 2>&1; then
    sudo rm -f /var/log/ovn/*.log.log*
    echo "  cleaned up stale .log.log files"
fi

LOGROTATE_FILE="/etc/logrotate.d/ovn-spinifex"
sudo tee "$LOGROTATE_FILE" >/dev/null <<'LOGROTATE'
/var/log/ovn/ovn-controller.log
/var/log/ovn/ovn-northd.log
/var/log/ovn/ovsdb-server-nb.log
/var/log/ovn/ovsdb-server-sb.log
{
    daily
    rotate 3
    maxsize 100M
    compress
    missingok
    notifempty
    copytruncate
}
LOGROTATE
echo "  logrotate config: $LOGROTATE_FILE (daily, 100M max, 3 rotations, explicit filenames)"

# --- Step 10: Disable auto-start on boot ---
# start-dev.sh / stop-dev.sh manage the OVN lifecycle. Without spinifex services
# running, ovn-controller spins in a tight reconnect loop burning CPU.
echo ""
echo "Step 10: Disabling OVN auto-start on boot (start-dev.sh will manage lifecycle)..."
sudo systemctl disable openvswitch-switch 2>/dev/null || true
sudo systemctl disable ovn-controller 2>/dev/null || true
echo "  openvswitch-switch: disabled on boot"
echo "  ovn-controller: disabled on boot"

# --- Step 11: Health check ---
echo ""
echo "Step 11: Verifying setup..."

OK=true

# Check br-int
if sudo ovs-vsctl br-exists br-int; then
    echo "  br-int:          OK"
else
    echo "  br-int:          FAILED"
    OK=false
fi

# Check WAN bridge (only if configured)
if [ -n "$WAN_BRIDGE" ]; then
    if sudo ovs-vsctl br-exists "$WAN_BRIDGE"; then
        echo "  $WAN_BRIDGE:$(printf '%*s' $((15 - ${#WAN_BRIDGE})) '') OK"
        if [ "$WAN_BRIDGE_MODE" = "direct" ]; then
            if sudo ovs-vsctl port-to-br "$WAN_IFACE" >/dev/null 2>&1; then
                echo "  direct bridge:   OK ($WAN_IFACE on $WAN_BRIDGE)"
            else
                echo "  direct bridge:   FAILED ($WAN_IFACE not on $WAN_BRIDGE)"
                OK=false
            fi
        elif [ "$WAN_BRIDGE_MODE" = "macvlan" ]; then
            MACVLAN_NAME="spx-ext-${WAN_IFACE}"
            if ip link show "$MACVLAN_NAME" >/dev/null 2>&1; then
                echo "  macvlan:         OK ($MACVLAN_NAME)"
            else
                echo "  macvlan:         FAILED ($MACVLAN_NAME not found)"
                OK=false
            fi
        fi
    else
        echo "  $WAN_BRIDGE:$(printf '%*s' $((15 - ${#WAN_BRIDGE})) '') FAILED"
        OK=false
    fi
fi

# Check ovn-controller
if sudo ovs-appctl -t ovn-controller version >/dev/null 2>&1 || systemctl is-active --quiet ovn-controller 2>/dev/null; then
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
