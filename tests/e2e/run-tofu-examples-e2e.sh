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
OPENTOFU_VERSION="${OPENTOFU_VERSION:-1.11.5}"

# awsgw and predastore are bound to the WAN IP (bootstrap-install passes --bind
# ${PRIMARY_WAN}), not loopback. Workbooks need the WAN IP for both the tofu
# provider endpoints and any CLI assertions that talk to S3 (:8443).
WAN_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')
SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR)

log() { echo "[$(date +%H:%M:%S)] $*"; }

install_tofu() {
    command -v tofu >/dev/null 2>&1 && return 0
    local arch
    case "$(uname -m)" in
        x86_64)  arch=amd64 ;;
        aarch64) arch=arm64 ;;
        *) log "unsupported arch: $(uname -m)"; return 1 ;;
    esac
    local url="https://github.com/opentofu/opentofu/releases/download/v${OPENTOFU_VERSION}/tofu_${OPENTOFU_VERSION}_linux_${arch}.tar.gz"
    log "Installing OpenTofu ${OPENTOFU_VERSION} (${arch})"
    local tmp
    tmp=$(mktemp -d)
    curl -fsSL "$url" | tar -xz -C "$tmp" tofu
    sudo install -m 0755 "$tmp/tofu" /usr/local/bin/tofu
    rm -rf "$tmp"
}

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

# Wait up to $2 seconds (default 150) for a 200 response from $1.
wait_for_http_200() {
    local url="$1" budget="${2:-150}"
    local attempts=$((budget / 5))
    for _ in $(seq 1 "$attempts"); do
        local status
        status=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null || echo "000")
        if [ "$status" = "200" ]; then
            return 0
        fi
        sleep 5
    done
    return 1
}

# Wait up to $2 seconds for the target group $1 to report at least one healthy target.
wait_for_alb_healthy() {
    local tg_arn="$1" budget="${2:-300}"
    local attempts=$((budget / 10))
    for _ in $(seq 1 "$attempts"); do
        local healthy
        healthy=$(aws elbv2 describe-target-health --target-group-arn "$tg_arn" \
            --query 'length(TargetHealthDescriptions[?TargetHealth.State==`healthy`])' \
            --output text 2>/dev/null || echo "0")
        if [ "$healthy" -gt 0 ] 2>/dev/null; then
            return 0
        fi
        sleep 10
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
    # sshd accepts before cloud-init finishes writing ~/.ssh/bastion-demo.pem.
    # Wait up to 180s for user_data to drop the key before hopping.
    if ! ssh "${SSH_OPTS[@]}" -i "$key" "ec2-user@${bastion}" \
        'for _ in $(seq 1 36); do [ -s ~/.ssh/bastion-demo.pem ] && exit 0; sleep 5; done; exit 1'; then
        log "  bastion: ~/.ssh/bastion-demo.pem never appeared (cloud-init stalled?)"
        return 1
    fi
    ssh "${SSH_OPTS[@]}" -i "$key" "ec2-user@${bastion}" \
        "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i ~/.ssh/bastion-demo.pem ec2-user@${private} id" \
        | grep -q '^uid='
}

dump_nginx_alb_diagnostics() {
    local tg_arn="${1:-}"
    local alb_ip="${2:-}"

    if [ -n "$alb_ip" ]; then
        log "  --- curl -v http://${alb_ip}/ (final attempt) ---"
        curl -vk --max-time 10 "http://${alb_ip}/" 2>&1 | head -100 || true
    fi

    log "  --- elbv2 system instances (spinifex:managed-by=elbv2) ---"
    aws ec2 describe-instances \
        --filters 'Name=tag:spinifex:managed-by,Values=elbv2' \
        --query 'Reservations[].Instances[].[InstanceId,State.Name,PrivateIpAddress,PublicIpAddress,StateReason.Message]' \
        --output table || true
    log "  --- all running instances ---"
    aws ec2 describe-instances \
        --query 'Reservations[].Instances[].[InstanceId,State.Name,StateReason.Message,PrivateIpAddress,PublicIpAddress,Tags[?Key==`Name`].Value|[0]]' \
        --output table || true
    if [ -n "$tg_arn" ]; then
        log "  --- target health ---"
        aws elbv2 describe-target-health --target-group-arn "$tg_arn" || true
        log "  --- registered target instances (full state) ---"
        aws elbv2 describe-target-health --target-group-arn "$tg_arn" \
            --query 'TargetHealthDescriptions[].Target.Id' --output text 2>/dev/null | tr '\t' '\n' | \
        while read -r tgt_id; do
            [ -z "$tgt_id" ] && continue
            aws ec2 describe-instances --instance-ids "$tgt_id" \
                --query 'Reservations[].Instances[].[InstanceId,State.Name,StateReason.Code,StateReason.Message,PrivateIpAddress,PublicIpAddress]' \
                --output table || true
        done
        log "  --- curl each backend public IP directly (isolates lb-agent vs backend) ---"
        aws elbv2 describe-target-health --target-group-arn "$tg_arn" \
            --query 'TargetHealthDescriptions[].Target.Id' --output text 2>/dev/null | tr '\t' '\n' | \
        while read -r tgt_id; do
            [ -z "$tgt_id" ] && continue
            local pub
            pub=$(aws ec2 describe-instances --instance-ids "$tgt_id" \
                --query 'Reservations[0].Instances[0].PublicIpAddress' --output text 2>/dev/null)
            if [ -n "$pub" ] && [ "$pub" != "None" ]; then
                local code
                code=$(curl -sk --max-time 5 -o /dev/null -w '%{http_code}' "http://${pub}/" 2>&1 || echo "curl-failed")
                log "    backend ${tgt_id} @ ${pub}: HTTP ${code}"
            else
                log "    backend ${tgt_id}: no public IP"
            fi
        done
    fi
    log "  --- daemon: lb-agent-specific lines ---"
    sudo journalctl -u spinifex-daemon --since '15 min ago' --no-pager 2>/dev/null | \
        grep -iE 'LB agent gateway URL|LBAgentHeartbeat|lbagent|lb-agent|healthrep|target health changed|failed launch' | tail -80 || true
    log "  --- daemon: errors/panics/fatal since workbook started ---"
    sudo journalctl -u spinifex-daemon --since '15 min ago' --no-pager 2>/dev/null | \
        grep -iE 'panic|fatal|ERROR|level=error' | tail -80 || true
    log "  --- daemon: restarts in last 15 min ---"
    sudo journalctl -u spinifex-daemon --since '15 min ago' --no-pager 2>/dev/null | \
        grep -iE 'Started |Stopping|Stopped|Main PID|killed' | tail -40 || true
    log "  --- viperblock: AccessDenied / nbdkit / zero-data errors ---"
    sudo journalctl -u spinifex-viperblock --since '15 min ago' --no-pager 2>/dev/null | \
        grep -iE 'AccessDenied|signature does not match|nbdkit|Broken pipe|NBDKit exited|zero data' | tail -60 || true
    log "  --- awsgw: errors ---"
    sudo journalctl -u spinifex-awsgw --since '15 min ago' --no-pager 2>/dev/null | \
        grep -iE 'level=error|InternalError|status":5' | tail -40 || true
}

assert_nginx_alb() {
    # ALB DNS (*.elb.spinifex.local) isn't resolvable from the host — README
    # documents fetching the public IP via elbv2 describe-load-balancers.
    local name ip tg_arn attempt
    name=$(tofu output -raw alb_name)

    # Retry describe-load-balancers: the gateway intermittently returns
    # InternalError during ALB provisioning while the lb-agent VM is booting.
    ip=""
    for attempt in 1 2 3 4 5 6; do
        ip=$(aws elbv2 describe-load-balancers --names "$name" \
            --query 'LoadBalancers[0].AvailabilityZones[].LoadBalancerAddresses[].IpAddress' \
            --output text 2>/dev/null | awk '{print $1}')
        if [ -n "$ip" ] && [ "$ip" != "None" ]; then
            break
        fi
        log "  nginx-alb: describe-load-balancers attempt ${attempt} returned no IP, retrying in 10s"
        sleep 10
    done
    if [ -z "$ip" ] || [ "$ip" = "None" ]; then
        log "  nginx-alb: no public IP for ${name} after retries"
        dump_nginx_alb_diagnostics ""
        return 1
    fi
    log "  nginx-alb: ALB ${name} public IP ${ip}"

    tg_arn=$(aws elbv2 describe-target-groups --names nginx-alb-tg \
        --query 'TargetGroups[0].TargetGroupArn' --output text 2>&1)
    log "  nginx-alb: target group ARN: ${tg_arn}"
    if [ -z "$tg_arn" ] || [[ "$tg_arn" == *Error* ]] || [[ "$tg_arn" == None ]]; then
        log "  nginx-alb: could not resolve target group ARN, aborting"
        dump_nginx_alb_diagnostics ""
        return 1
    fi
    if ! wait_for_alb_healthy "$tg_arn" 300; then
        log "  nginx-alb: no healthy targets after 300s — dumping diagnostics"
        dump_nginx_alb_diagnostics "$tg_arn" "$ip"
        return 1
    fi

    if ! wait_for_http_200 "http://${ip}/" 300; then
        log "  nginx-alb: targets healthy but no HTTP 200 after 300s — dumping diagnostics"
        dump_nginx_alb_diagnostics "$tg_arn" "$ip"
        return 1
    fi
}

assert_nginx_webserver() {
    local ip
    ip=$(tofu output -raw public_ip)
    wait_for_http_200 "http://${ip}/"
}

assert_s3_webapp() {
    # aws s3 goes to predastore on :8443, not the awsgw on :9999 the default
    # profile points at — the workbook's provider sets s3 = predastore_endpoint
    # but the CLI doesn't inherit that.
    local bucket sentinel endpoint
    bucket=$(tofu output -raw bucket_name)
    endpoint="https://${WAN_IP}:8443"
    sentinel="spinifex-nightly-$(date +%s).txt"
    echo "nightly-smoke" | aws s3 cp --endpoint-url "$endpoint" - "s3://${bucket}/${sentinel}" >/dev/null
    aws s3 ls --endpoint-url "$endpoint" "s3://${bucket}/" | grep -q "$sentinel"
}

# Pick an instance type available on this cluster. Workbooks default to
# t3.small (Intel); on AMD-only hosts t3 isn't registered, so we query and
# fall back to the smallest type with ≥2 vCPU / ≥1 GiB RAM (matches the
# nginx-alb README's documented approach).
detect_instance_type() {
    local endpoint="https://${WAN_IP}:9999"
    local picked
    picked=$(aws --endpoint-url "$endpoint" ec2 describe-instance-types \
        --query "sort_by(InstanceTypes[?VCpuInfo.DefaultVCpus==\`2\` && MemoryInfo.SizeInMiB>=\`1024\`], &MemoryInfo.SizeInMiB)[0].InstanceType" \
        --output text 2>/dev/null || true)
    if [ -z "$picked" ] || [ "$picked" = "None" ]; then
        picked=$(aws --endpoint-url "$endpoint" ec2 describe-instance-types \
            --query 'InstanceTypes[0].InstanceType' --output text 2>/dev/null || true)
    fi
    echo "$picked"
}

INSTANCE_TYPE=""

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

    local apply_args=(
        -input=false -no-color
        "-var=spinifex_endpoint=https://${WAN_IP}:9999"
        "-var=instance_type=${INSTANCE_TYPE}"
    )

    # s3-webapp has three required-no-default vars; creds come from
    # AWS_PROFILE=spinifex so the workbook's boto3 client authenticates.
    if [ "$example" = "s3-webapp" ]; then
        local access_key secret_key
        access_key=$(aws configure get aws_access_key_id --profile spinifex)
        secret_key=$(aws configure get aws_secret_access_key --profile spinifex)
        apply_args+=(
            "-var=predastore_endpoint=https://${WAN_IP}:8443"
            "-var=predastore_host=${WAN_IP}:8443"
            "-var=s3_access_key=${access_key}"
            "-var=s3_secret_key=${secret_key}"
        )
    fi

    if ! tofu init -input=false -no-color; then
        log "  FAIL ${example}: tofu init"
        return 1
    fi

    if ! tofu apply -auto-approve "${apply_args[@]}"; then
        log "  FAIL ${example}: tofu apply"
        tofu destroy -auto-approve "${apply_args[@]}" >/dev/null 2>&1 || true
        return 1
    fi

    local rc=0
    if assert_"${example//-/_}"; then
        log "  PASS ${example}"
    else
        log "  FAIL ${example}: assertion"
        rc=1
    fi

    tofu destroy -auto-approve "${apply_args[@]}" >/dev/null 2>&1 || \
        log "  WARN ${example}: tofu destroy failed"

    cd "$SCRIPT_DIR"
    return "$rc"
}

# --- Main ---

install_tofu || { log "tofu install failed"; exit 1; }

INSTANCE_TYPE=$(detect_instance_type)
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" = "None" ]; then
    log "no instance type available from describe-instance-types"
    exit 1
fi
log "Using instance_type=${INSTANCE_TYPE}"

for workbook in nginx-alb bastion-private-subnet nginx-webserver s3-webapp; do
    if ! run_workbook "$workbook"; then
        log "FAIL ${workbook} — aborting remaining workbooks"
        exit 1
    fi
done

log "All workbooks passed"
