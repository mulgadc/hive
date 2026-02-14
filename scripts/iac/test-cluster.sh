#!/usr/bin/env bash
set -euo pipefail

# Run smoke tests against a configured Hive cluster.
# Usage: test-cluster.sh <cluster_name> <state_file>

CLUSTER_NAME="${1:?Usage: test-cluster.sh <cluster_name> <state_file>}"
STATE_FILE="${2:?Usage: test-cluster.sh <cluster_name> <state_file>}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOFU_DIR="$SCRIPT_DIR/proxmox"

inventory=$(tofu -chdir="$TOFU_DIR" output -json -state="$STATE_FILE" inventory)
NODE1_IP=$(echo "$inventory" | jq -r '.nodes[0].management')

AWS_ENDPOINT="https://$NODE1_IP:9999"
PASS=0
FAIL=0

run_test() {
    local name="$1"
    shift
    printf "  %-40s " "$name"
    if output=$("$@" 2>&1); then
        echo "PASS"
        PASS=$((PASS + 1))
    else
        echo "FAIL"
        echo "    $output" | head -5
        FAIL=$((FAIL + 1))
    fi
}

echo "==> Smoke tests for cluster '$CLUSTER_NAME'"
echo "    Endpoint: $AWS_ENDPOINT"
echo ""

# Wait for gateway to be reachable
echo "==> Waiting for AWS gateway..."
attempts=0
while ! curl -sk --connect-timeout 3 "$AWS_ENDPOINT" >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ $attempts -ge 30 ]; then
        echo "Error: Gateway not reachable at $AWS_ENDPOINT after 150 seconds"
        exit 1
    fi
    sleep 5
done
echo "    Gateway reachable"
echo ""

echo "==> Running tests..."

run_test "describe-regions" \
    aws --endpoint-url "$AWS_ENDPOINT" --no-verify-ssl --region ap-southeast-2 \
    ec2 describe-regions

run_test "describe-instance-types" \
    aws --endpoint-url "$AWS_ENDPOINT" --no-verify-ssl --region ap-southeast-2 \
    ec2 describe-instance-types --query 'InstanceTypes[0].InstanceType'

run_test "describe-instances" \
    aws --endpoint-url "$AWS_ENDPOINT" --no-verify-ssl --region ap-southeast-2 \
    ec2 describe-instances

echo ""
echo "==> Results: $PASS passed, $FAIL failed"

if [ $FAIL -gt 0 ]; then
    exit 1
fi
