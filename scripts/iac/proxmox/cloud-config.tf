data "local_file" "ssh_public_key" {
  filename = pathexpand(var.ssh_public_key_path)
}

resource "proxmox_virtual_environment_file" "cloud_config" {
  count = var.node_count

  content_type = "snippets"
  datastore_id = "local"
  node_name    = var.nodes[count.index % length(var.nodes)].name

  source_raw {
    data = <<EOF
#cloud-config
package_update: true
package_upgrade: true
packages:
  - qemu-guest-agent
  - net-tools
  - git
  - make
runcmd:
  - timedatectl set-timezone America/Los_Angeles
  - systemctl enable qemu-guest-agent
  - systemctl start qemu-guest-agent
  - ufw disable
  - sysctl -w net.core.rmem_max=4194304
  - sysctl -w net.core.wmem_max=4194304
  - usermod -aG kvm tf-user
  - echo "done" > /tmp/vendor-cloud-init-done
EOF

    file_name = "${var.cluster_name}-${count.index + 1}-cloud-config.yaml"
  }
}
