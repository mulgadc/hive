#!/usr/bin/env bash
set -euo pipefail

# Check total coverage meets minimum threshold.
# Usage: scripts/check-coverage.sh <coverprofile> <min_coverage> [quiet]

PROFILE="${1:?Usage: check-coverage.sh <coverprofile> <min_coverage> [quiet]}"
MIN="${2:-60.0}"
QUIET="${3:-}"

if [[ ! -s "$PROFILE" ]]; then
    echo "ERROR: No coverage data generated — tests may have failed to compile"
    exit 1
fi

TOTAL=$(go tool cover -func="$PROFILE" | tail -1 | awk '{print $NF}' | tr -d '%')

if [[ -z "$TOTAL" ]]; then
    echo "ERROR: No coverage data generated — tests may have failed to compile"
    exit 1
fi

if [[ -z "$QUIET" ]]; then
    echo ""
    echo "=== Total Coverage ==="
    go tool cover -func="$PROFILE" | tail -1
fi

PASS=$(echo "$TOTAL >= $MIN" | bc -l)
if [[ "$PASS" != "1" ]]; then
    echo "ERROR: Total coverage ${TOTAL}% is below minimum ${MIN}%"
    exit 1
fi

[[ -z "$QUIET" ]] && echo "Coverage ${TOTAL}% meets minimum ${MIN}% threshold"
exit 0
