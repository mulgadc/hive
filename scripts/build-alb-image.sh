#!/bin/bash
# build-alb-image.sh — Thin wrapper for backward compatibility
# Delegates to the generic build-system-image.sh with the ALB manifest.
exec "$(dirname "$0")/build-system-image.sh" "$(dirname "$0")/images/alb.conf" "$@"
