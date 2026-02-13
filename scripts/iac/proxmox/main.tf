terraform {
  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "0.52.0"
    }
  }
}

locals {
  nodes = { for idx, node in var.nodes : node.name => merge(node, { index = idx + 1 }) }
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
      for_each = local.nodes
      content {
        name    = node.key
        address = node.value.address
      }
    }
  }
}
