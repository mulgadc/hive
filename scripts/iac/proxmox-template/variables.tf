# --- Proxmox Connection ---

variable "proxmox_endpoint" {
  type        = string
  description = "Proxmox VE API endpoint URL (e.g. https://pve.example.com:8006/)"
}

variable "proxmox_node_name" {
  type        = string
  description = "Proxmox node name to build the template on (as shown in the Proxmox UI)"
}

variable "proxmox_node_address" {
  type        = string
  description = "SSH-reachable hostname or IP of the Proxmox node"
}

variable "proxmox_ssh_username" {
  type        = string
  description = "SSH username for Proxmox host access"
  default     = "terraform"
}

# --- SSH Keys ---
# Proxmox host and VM typically use different key pairs.
# - ssh_private_key_path  → authenticates to the Proxmox host (provider + qm commands)
# - vm_ssh_private_key_path → authenticates to the VM for provisioning (remote-exec)
# - ssh_public_key_path → injected into the VM via cloud-init

variable "ssh_private_key_path" {
  type        = string
  description = "Path to SSH private key for Proxmox host access"
}

variable "vm_ssh_private_key_path" {
  type        = string
  description = "Path to SSH private key for VM access (must match ssh_public_key_path)"
}

variable "ssh_public_key_path" {
  type        = string
  description = "Path to SSH public key injected into the VM via cloud-init"
}

# --- Base Image ---

variable "base_image" {
  type        = string
  description = "Proxmox image file ID for the base OS (e.g. local:iso/debian-12-genericcloud-amd64-20240211-1654.img)"
  default     = "local:iso/debian-12-genericcloud-amd64-20240211-1654.img"
}

variable "base_image_tag" {
  type        = string
  description = "Short tag identifying the base image, used in template tags for tracking"
  default     = "debian-12"
}

# --- Template Settings ---

variable "template_name" {
  type        = string
  description = "Name for the resulting Proxmox template"
  default     = "hive-node-template"
}

variable "template_vmid" {
  type        = number
  description = "VM ID for the template (pick a high number to avoid conflicts, e.g. 9000)"
  default     = 9000
}

variable "vm_ssh_user" {
  type        = string
  description = "Username created in the VM via cloud-init"
  default     = "tf-user"
}

# --- VM Resources ---

variable "cpu_cores" {
  type        = number
  description = "CPU cores for the builder VM (more = faster builds)"
  default     = 4
}

variable "memory_mb" {
  type        = number
  description = "Memory in MB for the builder VM"
  default     = 8192
}

variable "disk_size_gb" {
  type        = number
  description = "Disk size in GB (needs room for OS + Go + modules + cloud images)"
  default     = 32
}

variable "datastore_id" {
  type        = string
  description = "Proxmox storage for the VM disk"
  default     = "local-lvm"
}

variable "bridge" {
  type        = string
  description = "Network bridge for the VM"
  default     = "vmbr0"
}

# --- Git ---

variable "git_branch" {
  type        = string
  description = "Git branch to clone for hive, viperblock, and predastore"
  default     = "main"
}
