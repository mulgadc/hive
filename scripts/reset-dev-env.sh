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

# Remove veth pair created by setup-ovn.sh (veth mode — Linux bridge ↔ OVS bridge)
if ip link show veth-wan-br >/dev/null 2>&1; then
    echo "  Deleting veth pair: veth-wan-br ↔ veth-wan-ovs"
    sudo ip link del veth-wan-br 2>/dev/null || true
fi

# Remove veth persistence units (Fix 1, mulga-998.b). Without this, systemd-networkd
# recreates the veth on next reboot even after a full dev reset.
if [ -e /etc/systemd/network/15-spinifex-veth-wan.netdev ] || \
   [ -e /etc/systemd/network/15-spinifex-veth-wan.network ]; then
    echo "  Deleting veth persistence units"
    sudo rm -f /etc/systemd/network/15-spinifex-veth-wan.netdev \
               /etc/systemd/network/15-spinifex-veth-wan.network
    sudo networkctl reload 2>/dev/null || true
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
# Detect WAN interface (default route).
WAN_IFACE=$(ip -4 route show default | awk '{print $5}' | head -1)
WAN_GW=$(ip -4 route show default | awk '{print $3}' | head -1)

echo "Detected WAN interface: ${WAN_IFACE:-none}, gateway: ${WAN_GW:-none}"

# Check if WAN interface is already a bridge (tofu-cluster / production setup).
# If so, setup-ovn.sh auto-detects it. If it's a physical NIC, we need to
# decide between direct bridge and macvlan based on SSH safety.
WAN_IS_BRIDGE=false
if [ -n "$WAN_IFACE" ]; then
    if ip -d link show "$WAN_IFACE" 2>/dev/null | grep -q "bridge"; then
        WAN_IS_BRIDGE=true
    fi
fi

# Build setup-ovn.sh flags based on detected topology
SETUP_OVN_FLAGS=""
if [ "$WAN_IS_BRIDGE" = true ]; then
    # WAN is already a bridge (e.g. br-wan from cloud-init) — setup-ovn.sh
    # auto-detects it, no explicit flags needed.
    echo "  WAN is a bridge: $WAN_IFACE (auto-detected)"
elif [ -n "$WAN_IFACE" ]; then
    # WAN is a physical NIC. Detect SSH NIC to decide bridge strategy.
    SSH_NIC=""
    if [ -n "${SSH_CONNECTION:-}" ]; then
        SSH_IP=$(echo "$SSH_CONNECTION" | awk '{print $3}')
        SSH_NIC=$(ip -o -4 addr show | awk -v ip="$SSH_IP" '$0 ~ ip"/" {print $2}' | head -1)
    fi
    if [ -z "$SSH_NIC" ]; then
        SSH_NIC="$WAN_IFACE"
    fi

    if [ "$WAN_IFACE" != "$SSH_NIC" ]; then
        SETUP_OVN_FLAGS="--wan-bridge=br-wan --wan-iface=$WAN_IFACE"
        echo "  Bridge mode: direct (WAN=$WAN_IFACE != SSH=$SSH_NIC)"
    else
        SETUP_OVN_FLAGS="--macvlan --wan-iface=$WAN_IFACE"
        echo "  Bridge mode: macvlan (WAN=$WAN_IFACE == SSH=$SSH_NIC)"
    fi
fi

# Chassis ID — let setup-ovn.sh auto-detect from hostname. This ensures the
# system-id matches what ovn-controller registers in the SBDB, which is what
# vpcd discovers at startup for gateway scheduling.

echo "Re-initializing OVN"
./scripts/setup-ovn.sh --management $SETUP_OVN_FLAGS

echo "Initializing platform"
ADMIN_INIT_ARGS="--region $REGION --az ${REGION}a --node node1 --nodes 1"

# External networking mode: set EXTERNAL_MODE=nat for NAT/DHCP mode (outbound-only),
# or leave unset / EXTERNAL_MODE=pool for pool mode (per-VM public IPs).
EXTERNAL_MODE="${EXTERNAL_MODE:-pool}"

if [ -n "$WAN_IFACE" ] && [ -n "$WAN_GW" ]; then
    WAN_IP=$(ip -4 -o addr show "$WAN_IFACE" 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)
    WAN_PREFIX=$(ip -4 -o addr show "$WAN_IFACE" 2>/dev/null | awk '{print $4}' | cut -d/ -f2 | head -1)
    if [ -z "$WAN_PREFIX" ]; then WAN_PREFIX=24; fi

    if [ "$EXTERNAL_MODE" = "nat" ]; then
        # NAT mode: outbound-only via shared SNAT. Use a single IP from the
        # high end of the WAN subnet as the gateway IP. No per-VM public IPs.
        IFS='.' read -r o1 o2 o3 o4 <<< "$WAN_GW"
        GATEWAY_IP="${GATEWAY_IP:-${o1}.${o2}.${o3}.200}"
        echo "  External mode: nat (gateway IP: $GATEWAY_IP, WAN gateway: $WAN_GW)"
        ADMIN_INIT_ARGS="$ADMIN_INIT_ARGS --external-mode=nat --gateway-ip=${GATEWAY_IP} --external-gateway=${WAN_GW} --external-prefix-len=${WAN_PREFIX}"
    else
        # Pool mode: per-VM public IPs from a static range
        IFS='.' read -r o1 o2 o3 o4 <<< "$WAN_GW"
        POOL_START="${o1}.${o2}.${o3}.200"
        POOL_END="${o1}.${o2}.${o3}.250"
        echo "  External pool: $POOL_START - $POOL_END (gateway: $WAN_GW)"
        ADMIN_INIT_ARGS="$ADMIN_INIT_ARGS --external-mode=pool --external-pool=${POOL_START}-${POOL_END} --external-gateway=${WAN_GW} --external-prefix-len=${WAN_PREFIX}"
    fi
fi

# Re-initialize with --force to regenerate credentials, certs, config, and
# update ~/.aws/credentials. Must carry the same args so external networking
# config isn't lost.
echo "Re-initializing platform..."
./bin/spx admin init --force $ADMIN_INIT_ARGS

# Generate SSH key if it doesn't exist
if [ ! -f ~/.ssh/spinifex-key.pub ]; then
    echo "Generating SSH key pair..."
    ssh-keygen -t ed25519 -f ~/.ssh/spinifex-key -N ""
fi

# Trust the Spinifex CA certificate so TLS connections to predastore/awsgw work
if [ -f "$HOME/spinifex/config/ca.pem" ]; then
    echo "Adding Spinifex CA certificate to system trust store..."
    sudo cp "$HOME/spinifex/config/ca.pem" /usr/local/share/ca-certificates/spinifex-ca.crt
    sudo update-ca-certificates
fi

# Enable pprof for development
#PPROF_ENABLED=1 PPROF_OUTPUT=/tmp/spinifex-vm.prof ./scripts/start-dev.sh --build
./scripts/start-dev.sh --build

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

# --- Launch a smoke-test instance ---
echo "Launching smoke-test instance..."

# Pick instance type based on CPU vendor
if grep -q 'AuthenticAMD' /proc/cpuinfo; then
    INSTANCE_TYPE="t3a.small"
else
    INSTANCE_TYPE="t3.small"
fi

# Get the AMI we just imported
AMI_ID=$(aws ec2 describe-images --query "Images[0].ImageId" --output text)
if [ -z "$AMI_ID" ] || [ "$AMI_ID" = "None" ]; then
    echo "❌ No AMI found, skipping instance launch"
    exit 0
fi

# Find a public subnet (first available)
SUBNET_ID=$(aws ec2 describe-subnets \
    --filters "Name=map-public-ip-on-launch,Values=true" \
    --query "Subnets[0].SubnetId" --output text 2>/dev/null)
# Fallback: just grab the first subnet if no public-tagged one exists
if [ -z "$SUBNET_ID" ] || [ "$SUBNET_ID" = "None" ]; then
    SUBNET_ID=$(aws ec2 describe-subnets --query "Subnets[0].SubnetId" --output text)
fi
if [ -z "$SUBNET_ID" ] || [ "$SUBNET_ID" = "None" ]; then
    echo "❌ No subnet found, skipping instance launch"
    exit 0
fi

echo "  AMI: $AMI_ID"
echo "  Instance type: $INSTANCE_TYPE"
echo "  Subnet: $SUBNET_ID"

INSTANCE_ID=$(aws ec2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name spinifex-key \
    --subnet-id "$SUBNET_ID" \
    --count 1 \
    --query 'Instances[0].InstanceId' --output text)

echo "✅ Reset complete — instance $INSTANCE_ID launched ($INSTANCE_TYPE)"
