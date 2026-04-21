#!/bin/bash
# run-tofu-examples-e2e.sh — smoke-test the public terraform workbooks.
#
# Invoked by the nightly matrix (cell #17) after bootstrap-install.sh has
# brought up a single-node cluster. For each workbook, the driver does a
# clean tofu init/apply, runs one smoke assertion, then tofu destroy.
#
# All assertions are minimal by design (one check per workbook) so the
# maintenance cost of keeping them in sync with workbook edits stays low.
#
# Expects the workbooks to be available at $WORKBOOK_DIR (defaults to the
# tarball-included ./workbooks/ directory beside this script).

set -u
cd "$(dirname "$0")"
SCRIPT_DIR="$(pwd)"

export AWS_PROFILE=spinifex
WORKBOOK_DIR="${WORKBOOK_DIR:-${SCRIPT_DIR}/workbooks}"
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR)

log() { echo "[$(date +%H:%M:%S)] $*"; }

cleanup() {
    EXIT_CODE=$?
    if [ "$EXIT_CODE" -ne 0 ]; then
        for svc in spinifex-nats spinifex-predastore spinifex-viperblock \
                   spinifex-daemon spinifex-awsgw spinifex-vpcd; do
            echo "=== ${svc} ==="
            sudo journalctl -u "${svc}" --no-pager -n 200 2>/dev/null || true
        done
    fi
    exit "$EXIT_CODE"
}
trap cleanup EXIT

# --- Per-workbook assertions ---

# Wait up to 150s for instance SSH readiness.
wait_for_ssh() {
    local key="$1" host="$2"
    for _ in $(seq 1 30); do
        if ssh "${SSH_OPTS[@]}" -i "$key" "ec2-user@${host}" true 2>/dev/null; then
            return 0
        fi
        sleep 5
    done
    return 1
}

# Wait up to 150s for a 200 response from $1.
wait_for_http_200() {
    local url="$1"
    for _ in $(seq 1 30); do
        local status
        status=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null || echo "000")
        if [ "$status" = "200" ]; then
            return 0
        fi
        sleep 5
    done
    return 1
}

assert_bastion_private_subnet() {
    local bastion private key
    bastion=$(tofu output -raw bastion_public_ip)
    private=$(tofu output -raw private_instance_ip)
    key="$(pwd)/bastion-demo.pem"
    chmod 600 "$key"

    wait_for_ssh "$key" "$bastion" || {
        log "  bastion SSH not ready"
        return 1
    }
    ssh "${SSH_OPTS[@]}" -i "$key" "ec2-user@${bastion}" \
        "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i ~/.ssh/bastion-demo.pem ec2-user@${private} id" \
        | grep -q '^uid='
}

assert_nginx_alb() {
    local dns
    dns=$(tofu output -raw alb_dns_name)
    wait_for_http_200 "http://${dns}/"
}

assert_nginx_webserver() {
    local ip
    ip=$(tofu output -raw public_ip)
    wait_for_http_200 "http://${ip}/"
}

assert_s3_webapp() {
    local bucket sentinel
    bucket=$(tofu output -raw bucket_name)
    sentinel="spinifex-nightly-$(date +%s).txt"
    echo "nightly-smoke" | aws s3 cp - "s3://${bucket}/${sentinel}" >/dev/null
    aws s3 ls "s3://${bucket}/" | grep -q "$sentinel"
}

# --- Workbook driver ---

run_workbook() {
    local example="$1"
    local path="${WORKBOOK_DIR}/${example}"

    if [ ! -d "$path" ]; then
        log "SKIP ${example}: ${path} not found"
        return 1
    fi

    log "=== ${example} ==="
    cd "$path"
    rm -rf .terraform terraform.tfstate* .terraform.lock.hcl

    if ! tofu init -input=false -no-color; then
        log "  FAIL ${example}: tofu init"
        return 1
    fi

    if ! tofu apply -auto-approve -input=false -no-color; then
        log "  FAIL ${example}: tofu apply"
        tofu destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || true
        return 1
    fi

    local rc=0
    if assert_"${example//-/_}"; then
        log "  PASS ${example}"
    else
        log "  FAIL ${example}: assertion"
        rc=1
    fi

    tofu destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || \
        log "  WARN ${example}: tofu destroy failed"

    cd "$SCRIPT_DIR"
    return "$rc"
}

# --- Main ---

FAILED=()
for workbook in bastion-private-subnet nginx-alb nginx-webserver s3-webapp; do
    if ! run_workbook "$workbook"; then
        FAILED+=("$workbook")
    fi
done

if [ "${#FAILED[@]}" -ne 0 ]; then
    log "Failed workbooks: ${FAILED[*]}"
    exit 1
fi

log "All workbooks passed"
