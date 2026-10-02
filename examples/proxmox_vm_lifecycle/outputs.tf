output "deployment_id" {
  value = organesson_deployment.lifecycle.id
}

output "vm_id" {
  value = organesson_virtual_machine.fedora.id
}

output "proxmox_vmid" {
  value = organesson_virtual_machine.fedora.proxmox_vmid
}

output "proxmox_node" {
  value = organesson_virtual_machine.fedora.proxmox_node
}
