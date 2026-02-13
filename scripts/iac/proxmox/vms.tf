resource "proxmox_virtual_environment_vm" "iac_mulgaos" {
  for_each = local.nodes

  name        = "iac-dev-mulgaos-${each.value.index}"
  description = "Managed by Terraform"
  tags        = ["terraform", "mulgaos"]
  node_name   = each.key

  initialization {
    datastore_id        = "local"
    vendor_data_file_id = proxmox_virtual_environment_file.iac_mulgaos_config[each.key].id

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
    cores = 4
    type  = "host"
  }

  memory {
    dedicated = 8192
  }

  agent {
    enabled = true
  }

  network_device {
    bridge = each.value.bridge
  }

  network_device {
    bridge = each.value.bridge
  }

  disk {
    file_id      = "local:iso/debian-12-genericcloud-amd64-20240211-1654.img"
    datastore_id = each.value.datastore_id
    interface    = "virtio0"
    iothread     = true
    discard      = "on"
    size         = 32
  }
}

output "iac_mulgaos_ips" {
  value = {
    for name, vm in proxmox_virtual_environment_vm.iac_mulgaos : name => {
      management = vm.ipv4_addresses[1][0]
      data       = vm.ipv4_addresses[2][0]
    }
  }
}
