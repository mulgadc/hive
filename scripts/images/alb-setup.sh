#!/bin/sh
# alb-setup.sh — ALB-specific chroot setup
#
# Runs inside the Alpine image chroot after packages are installed.
# Sets up HAProxy placeholder config and alb-agent OpenRC init script.
# The alb-agent polls the AWS gateway for config updates and reports health
# via SigV4-signed HTTP requests. Credentials come from cloud-init env vars.

set -e

# Create haproxy config directory and placeholder config.
mkdir -p /etc/haproxy
cat > /etc/haproxy/haproxy.cfg <<'EOF'
# Placeholder config — replaced by alb-agent on first config fetch
global
    daemon
    maxconn 256

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s
EOF

# Create alb-agent OpenRC init script
mkdir -p /etc/init.d
cat > /etc/init.d/alb-agent <<'INITSCRIPT'
#!/sbin/openrc-run

description="ALB Gateway Config Agent"
command="/usr/local/bin/alb-agent"
command_args="--lb-id=${ALB_LB_ID:-unknown} --gateway=${ALB_GATEWAY_URL} --access-key=${ALB_ACCESS_KEY} --secret-key=${ALB_SECRET_KEY}"
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

# Do NOT enable alb-agent at boot — cloud-init must write
# /etc/conf.d/alb-agent (with ALB_LB_ID, ALB_GATEWAY_URL, ALB_ACCESS_KEY,
# ALB_SECRET_KEY) before the service starts.
# Cloud-init runcmd starts the service after write_files.
