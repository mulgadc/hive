output "inventory" {
  value = {
    cluster_name = var.cluster_name
    node_count   = var.node_count
    nodes = [for i, vm in proxmox_virtual_environment_vm.hive_node : {
      name       = "${var.cluster_name}-${i + 1}"
      index      = i + 1
      management = vm.ipv4_addresses[1][0]
      data       = vm.ipv4_addresses[2][0]
    }]
    ssh_user     = "tf-user"
    ssh_key_path = trimsuffix(var.ssh_public_key_path, ".pub")
  }
}
