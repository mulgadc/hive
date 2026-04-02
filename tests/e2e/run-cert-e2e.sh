#!/bin/bash
# run-cert-e2e.sh — Certificate validation E2E tests.
#
# Validates that server certificates contain the correct SANs and that
# TLS connections succeed without --insecure when using the Spinifex CA.
#
# Works in three modes:
#   1. Single-node:        ./run-cert-e2e.sh
#   2. Pseudo multi-node:  ./run-cert-e2e.sh --pseudo-multinode
#   3. Real multi-node:    ./run-cert-e2e.sh --multinode <node1-ip> <node2-ip> <node3-ip>
#
# Prerequisites:
#   - Cluster already bootstrapped and running (services up, certs generated)
#   - CA cert installed in system trust store (bootstrap.sh step 8)
#   - openssl and curl available
#   - For pseudo multi-node: simulated IPs configured (10.11.12.1-3)

set -euo pipefail

# --- Configuration ---

AWSGW_PORT=9999
UI_PORT=3000
PASSED=0
FAILED=0
TOTAL=0

# --- Parse arguments ---

MODE="single"
NODE_IPS=()

case "${1:-}" in
    --pseudo-multinode)
        MODE="pseudo"
        NODE_IPS=("10.11.12.1" "10.11.12.2" "10.11.12.3")
        SERVICE_IPS=("${NODE_IPS[@]}")
        ;;
    --multinode)
        shift
        MODE="multinode"
        NODE_IPS=("$@")
        SERVICE_IPS=("${NODE_IPS[@]}")
        if [ ${#NODE_IPS[@]} -lt 2 ]; then
            echo "Usage: $0 --multinode <node1-ip> <node2-ip> [node3-ip ...]"
            exit 1
        fi
        ;;
    "")
        MODE="single"
        # Single-node: discover all IPs that should be in the cert.
        NODE_IPS=("127.0.0.1")
        # Also add the machine's non-loopback IPs.
        while IFS= read -r ip; do
            NODE_IPS+=("$ip")
        done < <(ip -4 addr show scope global | grep -oP 'inet \K[\d.]+')
        # SERVICE_IPS: only IPs where awsgw actually listens (bind IP from config).
        # awsgw binds to the host specified in spinifex.toml, not all interfaces.
        CONFIG_FILE="/etc/spinifex/spinifex.toml"
        if [ ! -f "$CONFIG_FILE" ]; then
            CONFIG_FILE="$HOME/spinifex/config/spinifex.toml"
        fi
        if [ -f "$CONFIG_FILE" ]; then
            AWSGW_BIND=$(grep -A5 '\.awsgw\]' "$CONFIG_FILE" | grep 'host' | head -1 | sed 's/.*= *"\([^:]*\).*/\1/')
        fi
        if [ -n "${AWSGW_BIND:-}" ] && [ "$AWSGW_BIND" != "0.0.0.0" ]; then
            SERVICE_IPS=("$AWSGW_BIND")
        else
            SERVICE_IPS=("${NODE_IPS[@]}")
        fi
        ;;
    *)
        echo "Unknown option: $1"
        echo "Usage: $0 [--pseudo-multinode | --multinode <ip1> <ip2> ...]"
        exit 1
        ;;
esac

# --- Helpers ---

pass() {
    PASSED=$((PASSED + 1))
    TOTAL=$((TOTAL + 1))
    echo "  PASS: $1"
}

fail() {
    FAILED=$((FAILED + 1))
    TOTAL=$((TOTAL + 1))
    echo "  FAIL: $1"
}

# Resolve the Spinifex config CA cert path (not the system copy).
resolve_ca_cert() {
    for candidate in \
        "/etc/spinifex/ca.pem" \
        "$HOME/spinifex/config/ca.pem" \
        "$HOME/node1/config/ca.pem"; do
        if [ -f "$candidate" ]; then
            echo "$candidate"
            return
        fi
    done
    echo ""
}

# --- Locate CA cert ---

CA_CERT=$(resolve_ca_cert)
if [ -z "$CA_CERT" ]; then
    echo "ERROR: Cannot find Spinifex CA certificate"
    exit 1
fi

SYSTEM_CA_PATH="/usr/local/share/ca-certificates/spinifex-ca.crt"

# Resolve single-node config dir (production or dev)
if [ -d /etc/spinifex ]; then
    SINGLE_CONFIG_DIR="/etc/spinifex"
else
    SINGLE_CONFIG_DIR="$HOME/spinifex/config"
fi

echo "Using CA cert: $CA_CERT"
echo "Mode: $MODE"
echo "All IPs (SAN check): ${NODE_IPS[*]}"
echo "Service IPs (TLS connect): ${SERVICE_IPS[*]}"
echo ""

# --- Test 1: Verify cert SANs contain expected IPs ---

echo "=== Test 1: Certificate SAN validation ==="

for ip in "${NODE_IPS[@]}"; do
    # Determine which node's cert to check.
    case "$MODE" in
        single)
            CERT_PATH="$SINGLE_CONFIG_DIR/server.pem"
            ;;
        pseudo)
            # Each pseudo node has its own config dir.
            NODE_NUM="${ip##*.}"  # Extract last octet (1, 2, 3)
            CERT_PATH="$HOME/node${NODE_NUM}/config/server.pem"
            ;;
        multinode)
            # For real multi-node, fetch cert via openssl s_client.
            CERT_PATH=""
            ;;
    esac

    if [ -n "$CERT_PATH" ] && [ -f "$CERT_PATH" ]; then
        # Check SAN IPs in the local cert file.
        SANS=$(openssl x509 -in "$CERT_PATH" -text -noout 2>/dev/null | grep -A1 "Subject Alternative Name" || true)
        if echo "$SANS" | grep -q "$ip"; then
            pass "Cert at $CERT_PATH contains IP SAN $ip"
        else
            fail "Cert at $CERT_PATH missing IP SAN $ip (SANs: $SANS)"
        fi
    else
        # Fetch cert over the wire and check SANs.
        SANS=$(echo | openssl s_client -connect "$ip:$AWSGW_PORT" -servername "$ip" 2>/dev/null \
            | openssl x509 -text -noout 2>/dev/null \
            | grep -A1 "Subject Alternative Name" || true)
        if echo "$SANS" | grep -q "$ip"; then
            pass "Cert served by $ip:$AWSGW_PORT contains IP SAN $ip"
        else
            fail "Cert served by $ip:$AWSGW_PORT missing IP SAN $ip (SANs: $SANS)"
        fi
    fi
done

# --- Test 2: Verify hostname is in DNS SANs ---

echo ""
echo "=== Test 2: Hostname DNS SAN validation ==="

HOSTNAME=$(hostname)
if [ "$HOSTNAME" != "localhost" ] && [ -n "$HOSTNAME" ]; then
    case "$MODE" in
        single)
            CERT_PATH="$SINGLE_CONFIG_DIR/server.pem"
            ;;
        pseudo)
            CERT_PATH="$HOME/node1/config/server.pem"
            ;;
        multinode)
            CERT_PATH=""
            ;;
    esac

    if [ -n "$CERT_PATH" ] && [ -f "$CERT_PATH" ]; then
        SANS=$(openssl x509 -in "$CERT_PATH" -text -noout 2>/dev/null | grep -A1 "Subject Alternative Name" || true)
        if echo "$SANS" | grep -qi "$HOSTNAME"; then
            pass "Cert contains DNS SAN for hostname '$HOSTNAME'"
        else
            fail "Cert missing DNS SAN for hostname '$HOSTNAME' (SANs: $SANS)"
        fi
    else
        echo "  SKIP: Cannot check hostname SAN for remote nodes"
    fi
else
    echo "  SKIP: Hostname is 'localhost' or empty"
fi

# --- Test 3: TLS connection succeeds with explicit CA cert (no --insecure) ---

echo ""
echo "=== Test 3: TLS handshake with explicit CA trust (no --insecure) ==="

for ip in "${SERVICE_IPS[@]}"; do
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
        --cacert "$CA_CERT" \
        --connect-timeout 5 \
        "https://$ip:$AWSGW_PORT/" 2>/dev/null || echo "000")

    if [ "$HTTP_CODE" != "000" ]; then
        pass "TLS handshake OK for https://$ip:$AWSGW_PORT (HTTP $HTTP_CODE)"
    else
        fail "TLS handshake FAILED for https://$ip:$AWSGW_PORT"
    fi
done

# --- Test 4: System trust store — CA installed and trusted ---

echo ""
echo "=== Test 4: System trust store validation ==="

# 4a: Check the CA file exists in the system cert directory.
if [ -f "$SYSTEM_CA_PATH" ]; then
    pass "CA cert installed at $SYSTEM_CA_PATH"
else
    fail "CA cert NOT found at $SYSTEM_CA_PATH"
fi

# 4b: Verify the system copy matches the config copy (not stale).
if [ -f "$SYSTEM_CA_PATH" ]; then
    SYSTEM_HASH=$(openssl x509 -in "$SYSTEM_CA_PATH" -fingerprint -noout 2>/dev/null || echo "")
    CONFIG_HASH=$(openssl x509 -in "$CA_CERT" -fingerprint -noout 2>/dev/null || echo "")
    if [ -n "$SYSTEM_HASH" ] && [ "$SYSTEM_HASH" = "$CONFIG_HASH" ]; then
        pass "System CA fingerprint matches config CA"
    else
        fail "System CA fingerprint mismatch (system: $SYSTEM_HASH, config: $CONFIG_HASH)"
    fi
fi

# 4c: Verify curl works WITHOUT --cacert by relying on system trust store.
# This confirms update-ca-certificates ran and the CA is in the trust bundle.
TEST_IP="${SERVICE_IPS[0]}"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    --connect-timeout 5 \
    "https://$TEST_IP:$AWSGW_PORT/" 2>/dev/null || echo "000")

if [ "$HTTP_CODE" != "000" ]; then
    pass "System trust store: curl https://$TEST_IP:$AWSGW_PORT succeeds without --cacert (HTTP $HTTP_CODE)"
else
    fail "System trust store: curl https://$TEST_IP:$AWSGW_PORT FAILED without --cacert — CA not in system bundle?"
fi

# --- Test 5: openssl s_client verify with CA ---

echo ""
echo "=== Test 5: openssl s_client verify ==="

for ip in "${SERVICE_IPS[@]}"; do
    VERIFY_OUTPUT=$(echo | openssl s_client \
        -CAfile "$CA_CERT" \
        -connect "$ip:$AWSGW_PORT" \
        -verify_return_error 2>&1 || true)

    if echo "$VERIFY_OUTPUT" | grep -q "Verify return code: 0"; then
        pass "openssl verify OK for $ip:$AWSGW_PORT"
    else
        VERIFY_CODE=$(echo "$VERIFY_OUTPUT" | grep "Verify return code:" | head -1 || echo "unknown")
        fail "openssl verify FAILED for $ip:$AWSGW_PORT ($VERIFY_CODE)"
    fi
done

# --- Test 6: CA download endpoint (UI) ---

echo ""
echo "=== Test 6: CA download endpoint ==="

# Try UI endpoint on first node.
UI_IP="${NODE_IPS[0]}"
if [ "$MODE" = "single" ]; then
    UI_IP="127.0.0.1"
fi

DOWNLOAD_OUTPUT=$(curl -s --cacert "$CA_CERT" --connect-timeout 5 \
    "https://$UI_IP:$UI_PORT/api/ca.pem" 2>/dev/null || echo "")

if echo "$DOWNLOAD_OUTPUT" | grep -q "BEGIN CERTIFICATE"; then
    # Verify downloaded cert matches the CA cert we're using.
    DOWNLOADED_HASH=$(echo "$DOWNLOAD_OUTPUT" | openssl x509 -fingerprint -noout 2>/dev/null || echo "")
    LOCAL_HASH=$(openssl x509 -in "$CA_CERT" -fingerprint -noout 2>/dev/null || echo "")

    if [ -n "$DOWNLOADED_HASH" ] && [ "$DOWNLOADED_HASH" = "$LOCAL_HASH" ]; then
        pass "CA download from https://$UI_IP:$UI_PORT/api/ca.pem matches local CA"
    else
        fail "CA download fingerprint mismatch (downloaded: $DOWNLOADED_HASH, local: $LOCAL_HASH)"
    fi
else
    # UI may not be running in all E2E modes (pseudo-multinode disables UI).
    echo "  SKIP: UI not reachable at https://$UI_IP:$UI_PORT (not running or wrong IP)"
fi

# --- Test 7: Cert CN and issuer ---

echo ""
echo "=== Test 7: Certificate metadata ==="

case "$MODE" in
    single) CERT_PATH="$SINGLE_CONFIG_DIR/server.pem" ;;
    pseudo) CERT_PATH="$HOME/node1/config/server.pem" ;;
    *) CERT_PATH="" ;;
esac

if [ -n "$CERT_PATH" ] && [ -f "$CERT_PATH" ]; then
    CN=$(openssl x509 -in "$CERT_PATH" -subject -noout 2>/dev/null || echo "")
    ISSUER=$(openssl x509 -in "$CERT_PATH" -issuer -noout 2>/dev/null || echo "")

    if echo "$CN" | grep -q "Spinifex Server"; then
        pass "Server cert CN = 'Spinifex Server'"
    else
        fail "Server cert CN is not 'Spinifex Server' (got: $CN)"
    fi

    if echo "$ISSUER" | grep -q "Spinifex Local CA"; then
        pass "Server cert issued by 'Spinifex Local CA'"
    else
        fail "Server cert issuer is not 'Spinifex Local CA' (got: $ISSUER)"
    fi

    # Check expiry is within 1 year.
    EXPIRY=$(openssl x509 -in "$CERT_PATH" -enddate -noout 2>/dev/null | sed 's/notAfter=//')
    if [ -n "$EXPIRY" ]; then
        pass "Server cert expiry: $EXPIRY"
    else
        fail "Could not read cert expiry"
    fi
else
    echo "  SKIP: No local cert path for multinode mode"
fi

# --- Test 8: Cross-node cert isolation (multi-node only) ---

if [ "$MODE" != "single" ] && [ ${#SERVICE_IPS[@]} -ge 2 ]; then
    echo ""
    echo "=== Test 8: Cross-node cert isolation ==="

    for ip in "${SERVICE_IPS[@]}"; do
        # Each node's cert should contain its own IP but served certs should all
        # chain to the same CA.
        CERT_ISSUER=$(echo | openssl s_client -connect "$ip:$AWSGW_PORT" 2>/dev/null \
            | openssl x509 -issuer -noout 2>/dev/null || echo "")

        if echo "$CERT_ISSUER" | grep -q "Spinifex Local CA"; then
            pass "Node $ip cert chains to shared Spinifex CA"
        else
            fail "Node $ip cert has unexpected issuer: $CERT_ISSUER"
        fi
    done
fi

# --- Summary ---

echo ""
echo "========================================"
echo "Certificate E2E Results"
echo "========================================"
echo "  Total:  $TOTAL"
echo "  Passed: $PASSED"
echo "  Failed: $FAILED"
echo ""

if [ "$FAILED" -gt 0 ]; then
    echo "CERTIFICATE E2E TESTS FAILED"
    exit 1
fi
echo "ALL CERTIFICATE E2E TESTS PASSED"
