data "local_file" "ssh_public_key" {
  filename = pathexpand(var.ssh_public_key_path)
}

resource "proxmox_virtual_environment_file" "iac_mulgaos_config" {
  for_each = local.nodes

  content_type = "snippets"
  datastore_id = "local"
  node_name    = each.key

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
  - mkdir -p /home/tf-user/Development/mulga/
  - "cd /home/tf-user/Development/mulga && git clone https://github.com/mulgadc/hive.git && make -C hive quickinstall"
  - "echo 'export PATH=$PATH:/usr/local/go/bin/' >> /home/tf-user/.bashrc"
  - "export PATH=$PATH:/usr/local/go/bin && cd /home/tf-user/Development/mulga/hive && ./scripts/clone-deps.sh && ./scripts/dev-setup.sh"
  - chown -R tf-user:tf-user /home/tf-user/Development/
  - echo "done" > /tmp/vendor-cloud-init-done
EOF

    file_name = "iac-mulgaos-vendor-cloud-config.yaml"
  }
}
