#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOFU_DIR="$SCRIPT_DIR/proxmox"
CLUSTERS_DIR="$TOFU_DIR/clusters"

usage() {
    cat <<'USAGE'
Usage: hive-test.sh <command> <cluster_name> [options]

Commands:
  up          Provision VMs with OpenTofu
  configure   Clone repo, build, form cluster, start services
  test        Run smoke tests against the cluster
  down        Destroy VMs and clean up state
  full        up → configure → test → down (preserves test exit code)
  status      Show cluster health and node IPs
  ssh <N>     SSH to node N (1-based)

Options:
  --node-count=N     Number of VMs (default: 3)
  --memory-mb=N      Memory per VM in MB (default: 16384)
  --cpu-cores=N      CPU cores per VM (default: 4)
  --disk-size-gb=N   Disk size per VM in GB (default: 32)

Examples:
  ./scripts/iac/hive-test.sh up hive-test1
  ./scripts/iac/hive-test.sh configure hive-test1
  ./scripts/iac/hive-test.sh test hive-test1
  ./scripts/iac/hive-test.sh status hive-test1
  ./scripts/iac/hive-test.sh ssh hive-test1 2
  ./scripts/iac/hive-test.sh down hive-test1
  ./scripts/iac/hive-test.sh full hive-test1 --node-count=3
USAGE
    exit 1
}

[ $# -lt 2 ] && usage

COMMAND="$1"
CLUSTER_NAME="$2"
shift 2

# Parse options
NODE_COUNT=3
MEMORY_MB=16384
CPU_CORES=4
DISK_SIZE_GB=32
SSH_NODE=""

for arg in "$@"; do
    case "$arg" in
        --node-count=*) NODE_COUNT="${arg#*=}" ;;
        --memory-mb=*)  MEMORY_MB="${arg#*=}" ;;
        --cpu-cores=*)  CPU_CORES="${arg#*=}" ;;
        --disk-size-gb=*) DISK_SIZE_GB="${arg#*=}" ;;
        [0-9]*)         SSH_NODE="$arg" ;;
        *) echo "Unknown option: $arg"; usage ;;
    esac
done

STATE_DIR="$CLUSTERS_DIR/$CLUSTER_NAME"
STATE_FILE="$STATE_DIR/terraform.tfstate"

tofu_cmd() {
    tofu -chdir="$TOFU_DIR" "$@"
}

get_inventory() {
    tofu_cmd output -json -state="$STATE_FILE" inventory 2>/dev/null
}

get_node_ip() {
    local index=$1
    get_inventory | jq -r ".nodes[$((index - 1))].management"
}

get_ssh_key() {
    get_inventory | jq -r ".ssh_key_path"
}

get_node_count() {
    get_inventory | jq -r ".node_count"
}

cmd_up() {
    echo "==> Provisioning cluster '$CLUSTER_NAME' ($NODE_COUNT nodes, ${MEMORY_MB}MB RAM, ${CPU_CORES} cores)"
    mkdir -p "$STATE_DIR"

    tofu_cmd init

    tofu_cmd apply \
        -state="$STATE_FILE" \
        -var="cluster_name=$CLUSTER_NAME" \
        -var="node_count=$NODE_COUNT" \
        -var="memory_mb=$MEMORY_MB" \
        -var="cpu_cores=$CPU_CORES" \
        -var="disk_size_gb=$DISK_SIZE_GB" \
        -auto-approve

    echo ""
    echo "==> Cluster '$CLUSTER_NAME' provisioned"
    echo ""
    get_inventory | jq '.nodes[] | {name, management, data}'
}

cmd_configure() {
    echo "==> Configuring cluster '$CLUSTER_NAME'"
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No state file found at $STATE_FILE. Run 'up' first."
        exit 1
    fi

    "$SCRIPT_DIR/configure-cluster.sh" "$CLUSTER_NAME" "$STATE_FILE"
}

cmd_test() {
    echo "==> Testing cluster '$CLUSTER_NAME'"
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No state file found at $STATE_FILE. Run 'up' first."
        exit 1
    fi

    "$SCRIPT_DIR/test-cluster.sh" "$CLUSTER_NAME" "$STATE_FILE"
}

cmd_down() {
    echo "==> Destroying cluster '$CLUSTER_NAME'"
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No state file found at $STATE_FILE."
        exit 1
    fi

    tofu_cmd destroy \
        -state="$STATE_FILE" \
        -var="cluster_name=$CLUSTER_NAME" \
        -var="node_count=$NODE_COUNT" \
        -var="memory_mb=$MEMORY_MB" \
        -var="cpu_cores=$CPU_CORES" \
        -var="disk_size_gb=$DISK_SIZE_GB" \
        -auto-approve

    rm -rf "$STATE_DIR"
    echo "==> Cluster '$CLUSTER_NAME' destroyed"
}

cmd_full() {
    cmd_up

    cmd_configure

    local test_exit=0
    cmd_test || test_exit=$?

    cmd_down

    exit $test_exit
}

cmd_status() {
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No state file found at $STATE_FILE."
        exit 1
    fi

    local inventory
    inventory=$(get_inventory)
    local count
    count=$(echo "$inventory" | jq -r '.node_count')
    local ssh_key
    ssh_key=$(echo "$inventory" | jq -r '.ssh_key_path')

    echo "==> Cluster '$CLUSTER_NAME' status ($count nodes)"
    echo ""

    for i in $(seq 1 "$count"); do
        local ip
        ip=$(echo "$inventory" | jq -r ".nodes[$((i - 1))].management")
        local data_ip
        data_ip=$(echo "$inventory" | jq -r ".nodes[$((i - 1))].data")
        local name
        name=$(echo "$inventory" | jq -r ".nodes[$((i - 1))].name")

        printf "  %-20s mgmt=%-16s data=%-16s " "$name" "$ip" "$data_ip"

        local health
        if health=$(curl -s --connect-timeout 3 "http://$ip:4432/health" 2>/dev/null); then
            echo "health=$health"
        else
            echo "health=unreachable"
        fi
    done
}

cmd_ssh() {
    if [ -z "$SSH_NODE" ]; then
        echo "Usage: hive-test.sh ssh <cluster_name> <node_number>"
        exit 1
    fi

    local ip
    ip=$(get_node_ip "$SSH_NODE")
    local ssh_key
    ssh_key=$(get_ssh_key)

    echo "==> SSH to $CLUSTER_NAME node $SSH_NODE ($ip)"
    ssh -i "$(eval echo "$ssh_key")" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        "tf-user@$ip"
}

case "$COMMAND" in
    up)        cmd_up ;;
    configure) cmd_configure ;;
    test)      cmd_test ;;
    down)      cmd_down ;;
    full)      cmd_full ;;
    status)    cmd_status ;;
    ssh)       cmd_ssh ;;
    *)         echo "Unknown command: $COMMAND"; usage ;;
esac
