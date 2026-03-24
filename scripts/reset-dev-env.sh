#!/bin/bash

# Reset dev environment (will purge all data)
# This script is for single-node dev environments only.

set -euo pipefail

CONFIG_FILE="$HOME/spinifex/config/spinifex.toml"

# --- Guard: refuse to run on multi-node clusters ---
if [ -f "$CONFIG_FILE" ]; then
    NODE_COUNT=$(grep -cE '^\[nodes\.[^.]+\]' "$CONFIG_FILE")
    if [ "$NODE_COUNT" -gt 1 ]; then
        echo "❌ Multi-node cluster detected ($NODE_COUNT nodes in $CONFIG_FILE)."
        echo "   This script only supports single-node dev environments."
        echo "   Reset each node individually or use 'spx admin cluster shutdown'."
        exit 1
    fi
fi

# Save region from existing config before we delete everything
REGION="ap-southeast-2"
if [ -f "$CONFIG_FILE" ]; then
    SAVED_REGION=$(grep -E '^\s*region\s*=' "$CONFIG_FILE" | tail -1 | sed 's/.*=\s*"\(.*\)"/\1/')
    if [ -n "$SAVED_REGION" ]; then
        REGION="$SAVED_REGION"
    fi
fi
echo "Using region: $REGION"

# --- Shutdown services ---
echo "Shutting down services"

if ! ./scripts/stop-dev.sh; then
    echo "❌ Failed to stop services. Aborting reset to prevent data loss."
    exit 1
fi

# Verify no spinifex-related processes are still running
PROCESS_PATTERN='(bin/spx|spinifex-ui|nats-server|predastore|viperblock|vpcd)'
remaining=$(pgrep -af "$PROCESS_PATTERN" | grep -v "reset-dev-env.sh" || true)
if [ -n "$remaining" ]; then
    echo "Waiting for remaining processes to exit..."
    timeout=30
    elapsed=0
    while pgrep -af "$PROCESS_PATTERN" | grep -qv "reset-dev-env.sh" 2>/dev/null; do
        if [ $elapsed -ge $timeout ]; then
            echo "❌ Processes still running after ${timeout}s:"
            pgrep -af "$PROCESS_PATTERN" | grep -v "reset-dev-env.sh" || true
            echo "   Kill them manually and re-run this script."
            exit 1
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
fi

# Verify no QEMU VMs are running
if pgrep -x qemu-system-x86_64 > /dev/null 2>&1; then
    echo "❌ QEMU instances still running. Cannot reset while VMs are active."
    echo "   Run './scripts/stop-dev.sh' or kill them manually."
    exit 1
fi

echo "All services confirmed stopped"

# --- Clean OVS/OVN ---
echo "Removing OVS bridges and config"

if command -v ovs-vsctl >/dev/null 2>&1; then
    # Ensure OVS is running so we can clean up
    sudo systemctl start openvswitch-switch 2>/dev/null || true
    sleep 1

    # Delete all OVS bridges (br-int, br-external, etc.)
    for br in $(sudo ovs-vsctl list-br 2>/dev/null); do
        echo "  Deleting bridge: $br"
        sudo ovs-vsctl --if-exists del-br "$br"
    done

    # Clear OVN external_ids
    sudo ovs-vsctl --if-exists clear Open_vSwitch . external_ids 2>/dev/null || true
    echo "  Cleared OVS external_ids"

    # Stop OVS again after cleanup
    sudo systemctl stop openvswitch-switch 2>/dev/null || true
fi

# Clean OVN databases (both Northbound and Southbound)
# Delete the DB files outright — setup-ovn.sh will restart ovn-central with fresh
# empty databases. This eliminates stale SB state (chassis entries, port bindings,
# datapath bindings) that accumulates across resets and causes ovn-controller to
# enter a commit failure loop ("OVNSB commit failed, force recompute").
echo "Removing OVN database files"
sudo systemctl stop ovn-central 2>/dev/null || true
sudo systemctl stop ovn-controller 2>/dev/null || true
if [ -d /var/lib/ovn ]; then
    sudo rm -f /var/lib/ovn/ovnnb_db.db /var/lib/ovn/ovnsb_db.db
    echo "  Deleted /var/lib/ovn/ovn{nb,sb}_db.db"
else
    echo "  /var/lib/ovn not found, skipping OVN DB cleanup"
fi

# Remove macvlan interfaces created by setup-ovn.sh
for iface in $(ip -o link show type macvlan 2>/dev/null | awk -F': ' '{print $2}' | grep '^spx-ext-'); do
    echo "  Deleting macvlan: $iface"
    sudo ip link del "$iface" 2>/dev/null || true
done

# --- Wipe data ---
echo "Removing ~/spinifex"
rm -rf ~/spinifex

# --- Re-initialize ---
# Detect WAN interface (default route) for external bridge.
WAN_IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)
WAN_GW=$(ip -4 route show default | awk '{print $3}' | head -1)

echo "Detected WAN interface: ${WAN_IFACE:-none}, gateway: ${WAN_GW:-none}"

# Detect SSH/management NIC: the interface carrying the active SSH session.
# If SSH_CONNECTION is set (remote session), extract the server IP and find its NIC.
# If not set (local console), fall back to the WAN NIC (assume single-NIC host).
SSH_NIC=""
if [ -n "${SSH_CONNECTION:-}" ]; then
    SSH_IP=$(echo "$SSH_CONNECTION" | awk '{print $3}')
    SSH_NIC=$(ip -o -4 addr show | awk -v ip="$SSH_IP" '$0 ~ ip"/" {print $2}' | head -1)
fi
if [ -z "$SSH_NIC" ]; then
    SSH_NIC="$WAN_IFACE"
fi
echo "Detected SSH/management NIC: ${SSH_NIC:-none}"

# Choose bridge mode: direct bridge when WAN NIC != SSH NIC (dedicated WAN NIC
# available). Macvlan fallback when they're the same (single-NIC host).
BRIDGE_MODE_FLAG=""
if [ -n "$WAN_IFACE" ] && [ "$WAN_IFACE" != "$SSH_NIC" ]; then
    BRIDGE_MODE_FLAG="--direct"
    echo "  Bridge mode: direct (WAN=$WAN_IFACE != SSH=$SSH_NIC)"
else
    echo "  Bridge mode: macvlan (WAN=$WAN_IFACE == SSH=$SSH_NIC)"
fi

# Chassis ID must match what vpcd derives from the node name in spinifex.toml.
# vpcd generates "chassis-{nodeName}" (e.g., chassis-node1) and uses it to schedule
# gateway chassis in the NB DB. ovn-controller registers in the SB DB using the OVS
# system-id. If they don't match, gateway traffic has no host to schedule to.
NODE_NAME="node1"
CHASSIS_ID="chassis-${NODE_NAME}"

echo "Re-initializing OVN (chassis-id: $CHASSIS_ID)"
if [ -n "$WAN_IFACE" ]; then
    echo "  Using $WAN_IFACE for br-external"
    ./scripts/setup-ovn.sh --management --external-bridge --external-iface="$WAN_IFACE" $BRIDGE_MODE_FLAG --chassis-id="$CHASSIS_ID"
else
    echo "  No WAN interface detected, management-only"
    ./scripts/setup-ovn.sh --management --chassis-id="$CHASSIS_ID"
fi

echo "Initializing platform"
ADMIN_INIT_ARGS="--region $REGION --az ${REGION}a --node node1 --nodes 1"
if [ -n "$WAN_IFACE" ] && [ -n "$WAN_GW" ]; then
    # Suggest pool range at high end of WAN subnet to avoid DHCP conflicts
    WAN_IP=$(ip -4 -o addr show "$WAN_IFACE" 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)
    WAN_PREFIX=$(ip -4 -o addr show "$WAN_IFACE" 2>/dev/null | awk '{print $4}' | cut -d/ -f2 | head -1)
    if [ -z "$WAN_PREFIX" ]; then WAN_PREFIX=24; fi

    # Use high range of subnet: .200-.250 for /24, avoid common DHCP ranges
    IFS='.' read -r o1 o2 o3 o4 <<< "$WAN_GW"
    POOL_START="${o1}.${o2}.${o3}.200"
    POOL_END="${o1}.${o2}.${o3}.250"

    echo "  External pool: $POOL_START - $POOL_END (gateway: $WAN_GW)"
    ADMIN_INIT_ARGS="$ADMIN_INIT_ARGS --external-mode=pool --external-pool=${POOL_START}-${POOL_END} --external-gateway=${WAN_GW} --external-prefix-len=${WAN_PREFIX}"
fi
./bin/spx admin init $ADMIN_INIT_ARGS

# Re-initialize platform (generates fresh credentials, certs, config, and updates ~/.aws/credentials)
echo "Re-initializing platform..."
./bin/spx admin init --force

# Generate SSH key if it doesn't exist
if [ ! -f ~/.ssh/spinifex-key.pub ]; then
    echo "Generating SSH key pair..."
    ssh-keygen -t ed25519 -f ~/.ssh/spinifex-key -N ""
fi

# Enable pprof for development
PPROF_ENABLED=1 PPROF_OUTPUT=/tmp/spinifex-vm.prof ./scripts/start-dev.sh --build

export AWS_PROFILE=spinifex

# Import SSH key
echo "Importing SSH key"
aws ec2 import-key-pair --key-name "spinifex-key" --public-key-material fileb://~/.ssh/spinifex-key.pub
aws ec2 describe-key-pairs

# Import AMI
echo "Importing AMI"

LOCAL_IMAGE="$HOME/images/ubuntu-24.04.img"
if [ -f "$LOCAL_IMAGE" ]; then
    echo "Using local image: $LOCAL_IMAGE"
    ARCH=$(uname -m)
    if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        IMG_ARCH="arm64"
    else
        IMG_ARCH="x86_64"
    fi
    ./bin/spx admin images import --file "$LOCAL_IMAGE" --distro ubuntu --version 24.04 --arch "$IMG_ARCH"
else
    # Download by name based on architecture
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        IMAGE_NAME="ubuntu-24.04-x86_64"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        IMAGE_NAME="ubuntu-24.04-arm64"
    else
        echo "Warning: Unknown architecture $ARCH, defaulting to x86_64"
        IMAGE_NAME="ubuntu-24.04-x86_64"
    fi
    echo "Downloading image: $IMAGE_NAME"
    ./bin/spx admin images import --name "$IMAGE_NAME"
fi

aws ec2 describe-images

echo "Reset complete, fresh AMI imported, proceed to creating instances"
