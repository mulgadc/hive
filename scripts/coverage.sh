#!/usr/bin/env bash
set -euo pipefail

# Run Go tests with coverage and report total code coverage.

COVERPROFILE=$(mktemp /tmp/hive-coverage-XXXXXX.out)
trap 'rm -f "$COVERPROFILE"' EXIT

echo "Running tests with coverage..."
echo ""
LOG_IGNORE=1 go test -timeout 120s -coverprofile="$COVERPROFILE" -covermode=atomic ./hive/... 2>&1

if [[ ! -s "$COVERPROFILE" ]]; then
    echo "No coverage data collected."
    exit 1
fi

echo ""
echo "=== Total ==="
go tool cover -func="$COVERPROFILE" | awk '/^total:/ { print $NF }'
