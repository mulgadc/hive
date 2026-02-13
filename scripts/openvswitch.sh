#!/bin/sh

### Setup openvswitch
echo "Install and enable OVS:"

sudo apt update
sudo apt install -y openvswitch-switch
sudo systemctl enable --now openvswitch-switch

echo "Sysctls (overlay-friendly, avoids rp_filter pain):"

sudo tee /etc/sysctl.d/99-mulga-vpc.conf >/dev/null <<'EOF'
net.ipv4.ip_forward=1
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
EOF
sudo sysctl --system

echo "1) Create br-int on BOTH hosts"

sudo ovs-vsctl --may-exist add-br br-int
sudo ip link set br-int up

echo "Creating TAP for tap-vm1"

# Consider, adduser hive, for all services used.
sudo ip tuntap add dev tap-vm1 mode tap user $USER
sudo ip link set tap-vm1 mtu 1450
sudo ip link set tap-vm1 up
ip link show tap-vm1

echo "Creating flows on host1"

sudo ovs-ofctl del-flows br-int

sudo ovs-vsctl --may-exist add-port br-int tap-vm1

# VM -> Geneve (set VNI)
sudo ovs-ofctl add-flow br-int "in_port=tap-vm1,actions=set_tunnel:10302,output:geneve-h2"

# Geneve -> VM (only that VNI)
sudo ovs-ofctl add-flow br-int "in_port=geneve-h2,tun_id=10302,actions=output:tap-vm1"

# Creating 
#echo "Creating flows on host2"

#sudo ovs-vsctl --may-exist add-port br-int tap-vm1

#sudo ovs-ofctl del-flows br-int

# VM -> Geneve (set VNI)
#sudo ovs-ofctl add-flow br-int "in_port=tap-vm1,actions=set_tunnel:10302,output:geneve-h1"

# Geneve -> VM (only that VNI)
#sudo ovs-ofctl add-flow br-int "in_port=geneve-h1,tun_id=10302,actions=output:tap-vm1"

