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
  description = "Proxmox hosts for VM placement (VMs are distributed round-robin)"

  validation {
    condition     = length(var.nodes) > 0
    error_message = "At least one Proxmox node must be defined."
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

variable "cluster_name" {
  type        = string
  description = "Name for the Hive cluster (used in VM names and tags)"

  validation {
    condition     = can(regex("^[a-z0-9-]+$", var.cluster_name))
    error_message = "cluster_name must contain only lowercase letters, numbers, and hyphens."
  }
}

variable "node_count" {
  type        = number
  description = "Number of Hive VMs to create"
  default     = 3

  validation {
    condition     = var.node_count >= 1 && var.node_count <= 10
    error_message = "node_count must be between 1 and 10."
  }
}

variable "cpu_cores" {
  type        = number
  description = "Number of CPU cores per VM"
  default     = 4
}

variable "memory_mb" {
  type        = number
  description = "Memory in MB per VM"
  default     = 16384
}

variable "disk_size_gb" {
  type        = number
  description = "Disk size in GB per VM"
  default     = 32
}

variable "os_image" {
  type        = string
  description = "Proxmox image file ID for VM boot disk"
  default     = "local:iso/debian-12-genericcloud-amd64-20240211-1654.img"
}
