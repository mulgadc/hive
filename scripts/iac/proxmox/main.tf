terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "0.52.0"
    }
  }
}

# API token is read from PROXMOX_VE_API_TOKEN env var automatically
provider "proxmox" {
  endpoint = var.proxmox_endpoint
  insecure = true

  ssh {
    agent       = false
    username    = var.proxmox_ssh_username
    private_key = file(pathexpand(var.ssh_private_key_path))

    dynamic "node" {
      for_each = var.nodes
      content {
        name    = node.value.name
        address = node.value.address
      }
    }
  }
}
