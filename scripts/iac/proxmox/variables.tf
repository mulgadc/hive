variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox VE API endpoint URL (e.g. https://pve.example.com:8006/)"

  validation {
    condition     = can(regex("^https?://", var.proxmox_endpoint))
    error_message = "proxmox_endpoint must be a valid URL starting with http:// or https://"
  }
}

variable "proxmox_ssh_username" {
  type        = string
  description = "SSH username for Proxmox host access"
  default     = "terraform"
}

variable "ssh_private_key_path" {
  type        = string
  description = "Path to SSH private key for Proxmox host access"

  validation {
    condition     = length(var.ssh_private_key_path) > 0
    error_message = "ssh_private_key_path must not be empty."
  }
}

variable "ssh_public_key_path" {
  type        = string
  description = "Path to SSH public key injected into VMs via cloud-init"

  validation {
    condition     = length(var.ssh_public_key_path) > 0
    error_message = "ssh_public_key_path must not be empty."
  }
}

variable "nodes" {
  type = list(object({
    name         = string
    address      = string
    bridge       = string
    datastore_id = string
  }))
  description = "Exactly 3 Proxmox nodes for the Hive cluster"

  validation {
    condition     = length(var.nodes) == 3
    error_message = "Exactly 3 nodes must be defined for the Hive cluster."
  }

  validation {
    condition     = alltrue([for n in var.nodes : length(n.name) > 0 && length(n.address) > 0])
    error_message = "Each node must have a non-empty name and address."
  }

  validation {
    condition     = length(distinct([for n in var.nodes : n.name])) == length(var.nodes)
    error_message = "Each node must have a unique name."
  }
}
