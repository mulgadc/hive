#!/bin/sh
# Helper script for spinifex-predastore.service
# Detects host, port, backend, and node-id from spinifex.toml
# then execs spx. Falls back to single-node defaults if no config found.

CONF=/etc/spinifex/spinifex.toml
BIND="0.0.0.0:8443"
NODE_ID="0"

if [ -f "$CONF" ]; then
    H=$(awk -F'"' '/\[nodes\..*\.predastore\]/{f=1} f&&/^host/{print $2;exit}' "$CONF")
    if [ -n "$H" ]; then
        BIND="$H"
        BACKEND="distributed"
    fi

    N=$(awk -F'= *' '/node_id/{gsub(/ /,"",$2);print $2;exit}' "$CONF")
    if [ -n "$N" ] && [ "$N" != "0" ]; then
        NODE_ID="$N"
    fi
fi

export SPINIFEX_PREDASTORE_HOST="${BIND%%:*}"
export SPINIFEX_PREDASTORE_PORT="${BIND##*:}"
export SPINIFEX_PREDASTORE_NODE_ID="$NODE_ID"

exec /usr/local/bin/spx service predastore start
