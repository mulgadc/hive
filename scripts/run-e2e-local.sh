#!/bin/bash
# Run E2E tests locally using Docker, matching the CI process (e2e.yml).
# Requires: Docker, /dev/kvm
#
# Usage:
#   ./scripts/run-e2e-local.sh              # Run both suites
#   ./scripts/run-e2e-local.sh single       # Single-node only
#   ./scripts/run-e2e-local.sh multi        # Multi-node only

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PARENT_DIR="$(cd "$PROJECT_ROOT/.." && pwd)"

IMAGE_NAME="hive-e2e:latest"

# Determine which suites to run
SUITE="${1:-both}"
case "$SUITE" in
    single|multi|both) ;;
    *)
        echo "Usage: $0 [single|multi|both]"
        exit 1
        ;;
esac

# Verify /dev/kvm exists
if [ ! -e /dev/kvm ]; then
    echo "ERROR: /dev/kvm not found. KVM is required for E2E tests."
    exit 1
fi

# Verify sibling repos exist, clone if missing
for dep in viperblock predastore; do
    if [ ! -d "$PARENT_DIR/$dep" ]; then
        echo "Sibling repo '$dep' not found at $PARENT_DIR/$dep"
        echo "Running clone-deps.sh..."
        "$PROJECT_ROOT/scripts/clone-deps.sh"
        break
    fi
done

# Build Docker image (same context as CI — parent directory)
echo "Building E2E Docker image..."
docker build \
    -t "$IMAGE_NAME" \
    -f "$PROJECT_ROOT/tests/e2e/Dockerfile.e2e" \
    "$PARENT_DIR"

SINGLE_PASS=""
MULTI_PASS=""

# Run single-node E2E
if [ "$SUITE" = "single" ] || [ "$SUITE" = "both" ]; then
    echo ""
    echo "========================================"
    echo "Running Single-Node E2E Tests"
    echo "========================================"
    if docker run --privileged --rm \
        -v /dev/kvm:/dev/kvm \
        --name hive-e2e-local \
        "$IMAGE_NAME"; then
        SINGLE_PASS="PASS"
        echo "Single-node E2E: PASS"
    else
        SINGLE_PASS="FAIL"
        echo "Single-node E2E: FAIL"
    fi
fi

# Run multi-node E2E
if [ "$SUITE" = "multi" ] || [ "$SUITE" = "both" ]; then
    echo ""
    echo "========================================"
    echo "Running Multi-Node E2E Tests"
    echo "========================================"
    if docker run --privileged --rm \
        -v /dev/kvm:/dev/kvm \
        --cap-add=NET_ADMIN \
        --name hive-multinode-e2e-local \
        "$IMAGE_NAME" \
        ./tests/e2e/run-multinode-e2e.sh; then
        MULTI_PASS="PASS"
        echo "Multi-node E2E: PASS"
    else
        MULTI_PASS="FAIL"
        echo "Multi-node E2E: FAIL"
    fi
fi

# Summary
echo ""
echo "========================================"
echo "E2E Results"
echo "========================================"
[ -n "$SINGLE_PASS" ] && echo "  Single-node: $SINGLE_PASS"
[ -n "$MULTI_PASS" ] && echo "  Multi-node:  $MULTI_PASS"

# Exit with failure if any suite failed
if [ "$SINGLE_PASS" = "FAIL" ] || [ "$MULTI_PASS" = "FAIL" ]; then
    exit 1
fi
