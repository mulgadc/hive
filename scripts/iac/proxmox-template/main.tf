terraform {
  required_version = ">= 1.6.0"

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

    node {
      name    = var.proxmox_node_name
      address = var.proxmox_node_address
    }
  }
}
