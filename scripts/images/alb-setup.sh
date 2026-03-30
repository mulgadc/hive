#!/bin/sh
# alb-setup.sh — ALB-specific chroot setup
#
# Runs inside the Alpine image chroot after packages are installed.
# Sets up HAProxy placeholder config and alb-agent OpenRC init script.
# The alb-agent runs an HTTP server on :8405 that receives config pushes
# and serves health/ping endpoints to the daemon over the VPC network.

set -e

# Create haproxy config directory and placeholder config.
# Port 8405 is NOT bound here — the alb-agent owns it via its HTTP server.
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
EOF

# Create alb-agent OpenRC init script
mkdir -p /etc/init.d
cat > /etc/init.d/alb-agent <<'INITSCRIPT'
#!/sbin/openrc-run

description="ALB HTTP Config Agent"
command="/usr/local/bin/alb-agent"
command_args="--lb-id=${ALB_LB_ID:-unknown} --listen=:8405"
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
# /etc/conf.d/alb-agent (with ALB_LB_ID) before the service starts.
# Cloud-init runcmd starts the service after write_files.
