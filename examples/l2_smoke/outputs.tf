output "vmids" {
  value = {
    node_a = organesson_virtual_machine.node_a.proxmox_vmid
    node_b = organesson_virtual_machine.node_b.proxmox_vmid
  }
}

output "nodes" {
  value = {
    node_a = organesson_virtual_machine.node_a.proxmox_node
    node_b = organesson_virtual_machine.node_b.proxmox_node
  }
}
