output "template_vmid" {
  description = "VM ID of the created template"
  value       = proxmox_virtual_environment_vm.builder.vm_id
}

output "template_node" {
  description = "Proxmox node where the template lives"
  value       = var.proxmox_node_name
}

output "base_image_tag" {
  description = "Base image tag for this template"
  value       = var.base_image_tag
}

# Ready-to-run command: export the template for distribution to other Proxmox nodes
output "vzdump_export_command" {
  description = "Run this to export the template (vzdump) for copying to other nodes"
  value       = <<-EOT
    ssh -i ${var.ssh_private_key_path} ${var.proxmox_ssh_username}@${var.proxmox_node_address} \
      'vzdump ${var.template_vmid} --dumpdir /tmp --mode stop --compress zstd'
  EOT
}

# After exporting, copy to each target node and restore
output "distribute_commands" {
  description = "Copy and restore the template on other Proxmox nodes"
  value       = <<-EOT
    # 1. Export from build node (${var.proxmox_node_name}):
    ssh -i ${var.ssh_private_key_path} ${var.proxmox_ssh_username}@${var.proxmox_node_address} \
      'vzdump ${var.template_vmid} --dumpdir /tmp --mode stop --compress zstd'

    # 2. Copy to each target node:
    scp -3 \
      -i ${var.ssh_private_key_path} \
      ${var.proxmox_ssh_username}@${var.proxmox_node_address}:/tmp/vzdump-qemu-${var.template_vmid}-*.vma.zst \
      ${var.proxmox_ssh_username}@<TARGET_NODE_ADDRESS>:/tmp/

    # 3. On each target node — restore and convert to template:
    ssh -i ${var.ssh_private_key_path} ${var.proxmox_ssh_username}@<TARGET_NODE_ADDRESS> \
      'qmrestore /tmp/vzdump-qemu-${var.template_vmid}-*.vma.zst <NEW_VMID> --storage <DATASTORE> && qm template <NEW_VMID>'
  EOT
}
