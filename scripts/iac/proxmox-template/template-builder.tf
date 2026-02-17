data "local_file" "ssh_public_key" {
  filename = pathexpand(var.ssh_public_key_path)
}

# Minimal cloud-init: just enough to bootstrap SSH + guest agent.
# Heavy provisioning is done via remote-exec after boot.
resource "proxmox_virtual_environment_file" "cloud_config" {
  content_type = "snippets"
  datastore_id = "local"
  node_name    = var.proxmox_node_name

  source_raw {
    # NOTE: #cloud-config MUST start at column 0 — cloud-init uses it as a magic header.
    data = <<EOF
#cloud-config
package_update: true
packages:
  - qemu-guest-agent
  - git
  - make
  - curl
  - sudo
  - ca-certificates
runcmd:
  - systemctl enable qemu-guest-agent
  - systemctl start qemu-guest-agent
EOF

    file_name = "hive-template-cloud-config.yaml"
  }
}

resource "proxmox_virtual_environment_vm" "builder" {
  name      = "${var.template_name}-builder"
  node_name = var.proxmox_node_name
  vm_id     = var.template_vmid

  tags = sort(["terraform", "hive-template", var.base_image_tag])

  initialization {
    datastore_id        = "local"
    vendor_data_file_id = proxmox_virtual_environment_file.cloud_config.id

    user_account {
      username = var.vm_ssh_user
      keys     = [trimspace(data.local_file.ssh_public_key.content)]
    }

    ip_config {
      ipv4 {
        address = "dhcp"
      }
    }
  }

  cpu {
    cores = var.cpu_cores
    type  = "host"
  }

  memory {
    dedicated = var.memory_mb
  }

  agent {
    enabled = true
  }

  network_device {
    bridge = var.bridge
  }

  disk {
    file_id      = var.base_image
    datastore_id = var.datastore_id
    interface    = "virtio0"
    iothread     = true
    discard      = "on"
    size         = var.disk_size_gb
  }

  # SSH connection to the VM for provisioning.
  # Uses the VM key (vm_ssh_private_key_path), NOT the Proxmox host key.
  # Waits for guest agent to report an IP, then connects.
  connection {
    type        = "ssh"
    user        = var.vm_ssh_user
    private_key = file(pathexpand(var.vm_ssh_private_key_path))
    host        = element([for addr in flatten(self.ipv4_addresses) : addr if !startswith(addr, "127.")], 0)
    timeout     = "10m"
  }

  # Copy provisioning script to VM
  provisioner "file" {
    source      = "${path.module}/scripts/provision.sh"
    destination = "/tmp/provision.sh"
  }

  # Run provisioning (installs deps, clones repos, caches modules, cleans up)
  provisioner "remote-exec" {
    inline = [
      "chmod +x /tmp/provision.sh",
      "GIT_BRANCH='${var.git_branch}' /tmp/provision.sh",
    ]
  }
}

# After provisioning completes, shut down the VM and convert to a Proxmox template.
# This runs on the Proxmox host via SSH (not on the VM — it's being shut down).
resource "null_resource" "convert_template" {
  depends_on = [proxmox_virtual_environment_vm.builder]

  triggers = {
    builder_id = proxmox_virtual_environment_vm.builder.vm_id
  }

  provisioner "local-exec" {
    command = <<-EOT
      ssh -i '${pathexpand(var.ssh_private_key_path)}' \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        '${var.proxmox_ssh_username}@${var.proxmox_node_address}' \
        "sudo qm shutdown ${proxmox_virtual_environment_vm.builder.vm_id} --timeout 60 && \
         echo 'Waiting for VM to stop...' && \
         while sudo qm status ${proxmox_virtual_environment_vm.builder.vm_id} | grep -q running; do sleep 2; done && \
         echo 'Converting to template...' && \
         sudo qm template ${proxmox_virtual_environment_vm.builder.vm_id} && \
         echo 'Template ${var.template_vmid} created successfully'"
    EOT
  }
}
