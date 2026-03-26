#!/bin/sh
# alb-setup.sh — ALB-specific chroot setup
#
# Runs inside the Alpine image chroot after packages are installed.
# Sets up HAProxy placeholder config and alb-agent OpenRC init script.

set -e

# Create haproxy config directory and placeholder config
mkdir -p /etc/haproxy
cat > /etc/haproxy/haproxy.cfg <<'EOF'
# Placeholder config — replaced by alb-agent on first config push
global
    daemon
    maxconn 256

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend health
    bind *:8405
    http-request return status 200 content-type text/plain string "ok"
EOF

# Create alb-agent OpenRC init script
mkdir -p /etc/init.d
cat > /etc/init.d/alb-agent <<'INITSCRIPT'
#!/sbin/openrc-run

description="ALB NATS Config Agent"
command="/usr/local/bin/alb-agent"
command_args="--lb-id=${ALB_LB_ID:-unknown} --nats=${ALB_NATS_URL:-nats://10.0.2.2:4222}"
command_background=true
pidfile="/run/alb-agent.pid"
output_log="/var/log/alb-agent.log"
error_log="/var/log/alb-agent.log"

depend() {
    need net
    after firewall
}
INITSCRIPT
chmod 755 /etc/init.d/alb-agent
