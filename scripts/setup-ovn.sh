#!/bin/bash

# OVN Compute Node Setup for Spinifex VPC Networking
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
#   --management       Also start OVN central services (NB DB, SB DB, ovn-northd)
#   --external-bridge  Create br-external for public subnet WAN uplink
#   --external-iface   WAN NIC to add to br-external (default: eth1)
#   --single-nic       Use macvlan instead of adding NIC directly (for single-NIC hosts)
#   --dhcp             Obtain gateway IP via DHCP on the external bridge interface
#   --ovn-remote       OVN SB DB address (default: tcp:127.0.0.1:6642)
#   --encap-ip         Geneve tunnel endpoint IP (default: auto-detect)
#   --chassis-id       OVN chassis identifier (default: hostname)
#
# Examples:
#   # Single-node development (management + compute on same host):
#   ./scripts/setup-ovn.sh --management
#
#   # With external bridge (dedicated WAN NIC):
#   ./scripts/setup-ovn.sh --management --external-bridge --external-iface=eth1
#
#   # With external bridge (single NIC — uses macvlan, SSH-safe):
#   ./scripts/setup-ovn.sh --management --external-bridge --external-iface=eth0 --single-nic
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
EXTERNAL_BRIDGE=false
SINGLE_NIC=false
EXTERNAL_DHCP=false
EXTERNAL_IFACE="eth1"
OVN_REMOTE="tcp:127.0.0.1:6642"
ENCAP_IP=""
CHASSIS_ID=""

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --management)       MANAGEMENT=true ;;
        --external-bridge)  EXTERNAL_BRIDGE=true ;;
        --single-nic)       SINGLE_NIC=true ;;
        --dhcp)             EXTERNAL_DHCP=true ;;
        --external-iface=*) EXTERNAL_IFACE="${arg#*=}" ;;
        --ovn-remote=*)     OVN_REMOTE="${arg#*=}" ;;
        --encap-ip=*)       ENCAP_IP="${arg#*=}" ;;
        --chassis-id=*)     CHASSIS_ID="${arg#*=}" ;;
        --help|-h)
            head -36 "$0" | tail -34
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

echo "=== Spinifex OVN Compute Node Setup ==="
echo "  Management node:  $MANAGEMENT"
echo "  External bridge:  $EXTERNAL_BRIDGE"
if [ "$EXTERNAL_BRIDGE" = true ]; then
echo "  External iface:   $EXTERNAL_IFACE"
echo "  Single NIC mode:  $SINGLE_NIC"
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

# --- Step 3b: Create br-external for WAN uplink (optional) ---
if [ "$EXTERNAL_BRIDGE" = true ]; then
    echo ""
    echo "Step 3b: Configuring br-external for public subnet WAN uplink..."

    # Verify the WAN NIC exists
    if ! ip link show "$EXTERNAL_IFACE" >/dev/null 2>&1; then
        echo "  ERROR: interface $EXTERNAL_IFACE does not exist"
        echo "  Available interfaces:"
        ip -o link show | awk -F': ' '{print "    " $2}'
        exit 1
    fi

    # Create br-external
    sudo ovs-vsctl --may-exist add-br br-external
    sudo ip link set br-external up

    if [ "$SINGLE_NIC" = true ]; then
        # --- macvlan strategy (single-NIC hosts) ---
        # Create a macvlan sub-interface in bridge mode off the host's NIC.
        # The host keeps its IP on the parent NIC — no migration, SSH-safe.
        # OVN localnet traffic flows through the macvlan to the physical wire.
        MACVLAN_NAME="spx-ext-${EXTERNAL_IFACE}"

        if ip link show "$MACVLAN_NAME" >/dev/null 2>&1; then
            echo "  macvlan $MACVLAN_NAME already exists"
        else
            sudo ip link add "$MACVLAN_NAME" link "$EXTERNAL_IFACE" type macvlan mode bridge
            echo "  created macvlan: $MACVLAN_NAME (bridge mode) on $EXTERNAL_IFACE"
        fi

        sudo ip link set "$MACVLAN_NAME" up
        sudo ovs-vsctl --may-exist add-port br-external "$MACVLAN_NAME"
        echo "  br-external: created with macvlan port $MACVLAN_NAME"
        echo "  NOTE: host keeps its IP on $EXTERNAL_IFACE (no migration)"
        echo "  QUIRK: host cannot reach VMs at their public IPs (macvlan isolation)"
    else
        # --- dedicated NIC / IP migration strategy ---
        # Add the WAN NIC directly to br-external.
        sudo ovs-vsctl --may-exist add-port br-external "$EXTERNAL_IFACE"

        # If the WAN NIC has an IP, move it to br-external so the host keeps
        # connectivity. This is the standard OpenStack/OVN approach for sharing
        # a NIC between host and OVN localnet traffic.
        WAN_IP=$(ip -4 addr show dev "$EXTERNAL_IFACE" 2>/dev/null | awk '/inet /{print $2}' | head -1)
        if [ -n "$WAN_IP" ]; then
            WAN_GW=$(ip -4 route show default dev "$EXTERNAL_IFACE" 2>/dev/null | awk '{print $3}' | head -1)
            # Capture DNS config from the original interface before migration
            WAN_DNS=$(resolvectl dns "$EXTERNAL_IFACE" 2>/dev/null | awk '{for(i=2;i<=NF;i++) printf $i" "}' | xargs)

            sudo ip addr del "$WAN_IP" dev "$EXTERNAL_IFACE" 2>/dev/null || true
            sudo ip addr add "$WAN_IP" dev br-external
            if [ -n "$WAN_GW" ]; then
                sudo ip route add default via "$WAN_GW" dev br-external 2>/dev/null || true
            fi

            # Fix DNS: systemd-resolved associates DNS servers with interfaces.
            # After IP migration, the resolver no longer knows which interface
            # reaches the DNS server. Point it at br-external.
            if [ -n "$WAN_DNS" ]; then
                sudo resolvectl dns br-external $WAN_DNS 2>/dev/null || true
                echo "  Migrated DNS ($WAN_DNS) to br-external"
            else
                # Fallback: set Google DNS on br-external
                sudo resolvectl dns br-external 8.8.8.8 2>/dev/null || true
                echo "  Set fallback DNS (8.8.8.8) on br-external"
            fi
            # Also set the search domain if it existed
            WAN_DOMAIN=$(resolvectl domain "$EXTERNAL_IFACE" 2>/dev/null | awk '{for(i=2;i<=NF;i++) printf $i" "}' | xargs)
            if [ -n "$WAN_DOMAIN" ]; then
                sudo resolvectl domain br-external $WAN_DOMAIN 2>/dev/null || true
            fi

            echo "  Migrated $WAN_IP from $EXTERNAL_IFACE to br-external"
        fi

        echo "  br-external: created with port $EXTERNAL_IFACE"
    fi

    # --- DHCP: obtain gateway IP for OVN SNAT ---
    if [ "$EXTERNAL_DHCP" = true ]; then
        echo ""
        echo "Step 3c: Obtaining external gateway IP via DHCP..."

        # Determine which interface to DHCP on
        if [ "$SINGLE_NIC" = true ]; then
            DHCP_IFACE="spx-ext-${EXTERNAL_IFACE}"
        else
            DHCP_IFACE="br-external"
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
                # Update gateway_ip in the config
                if grep -q "gateway_ip" "$CONFIG_FILE"; then
                    sed -i "s/gateway_ip.*/gateway_ip = \"$DHCP_IP\"/" "$CONFIG_FILE"
                else
                    # Insert gateway_ip after the gateway line in the pool section
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

BRIDGE_MAPPINGS="external:br-external"
if [ "$EXTERNAL_BRIDGE" = true ]; then
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

# Check br-external (only if --external-bridge was used)
if [ "$EXTERNAL_BRIDGE" = true ]; then
    if sudo ovs-vsctl br-exists br-external; then
        echo "  br-external:     OK"
        if [ "$SINGLE_NIC" = true ]; then
            MACVLAN_NAME="spx-ext-${EXTERNAL_IFACE}"
            if ip link show "$MACVLAN_NAME" >/dev/null 2>&1; then
                echo "  macvlan:         OK ($MACVLAN_NAME)"
            else
                echo "  macvlan:         FAILED ($MACVLAN_NAME not found)"
                OK=false
            fi
        fi
    else
        echo "  br-external:     FAILED"
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
