resource "proxmox_virtual_environment_vm" "hive_node" {
  count = var.node_count

  name        = "${var.cluster_name}-${count.index + 1}"
  description = "Managed by Terraform"
  tags        = ["terraform", "hive", var.cluster_name]
  node_name   = var.nodes[count.index % length(var.nodes)].name

  initialization {
    datastore_id        = "local"
    vendor_data_file_id = proxmox_virtual_environment_file.cloud_config[count.index].id

    user_account {
      username = "tf-user"
      keys     = [trimspace(data.local_file.ssh_public_key.content)]
    }

    # Management
    ip_config {
      ipv4 {
        address = "dhcp"
      }
    }

    # Data/VM
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
    bridge = var.nodes[count.index % length(var.nodes)].bridge
  }

  network_device {
    bridge = var.nodes[count.index % length(var.nodes)].bridge
  }

  disk {
    file_id      = var.os_image
    datastore_id = var.nodes[count.index % length(var.nodes)].datastore_id
    interface    = "virtio0"
    iothread     = true
    discard      = "on"
    size         = var.disk_size_gb
  }
}
