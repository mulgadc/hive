#!/bin/bash
# build-lb-image.sh — Thin wrapper for backward compatibility
# Delegates to the generic build-system-image.sh with the LB manifest.
exec "$(dirname "$0")/build-system-image.sh" "$(dirname "$0")/images/lb.conf" "$@"
