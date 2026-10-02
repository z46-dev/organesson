output "deployment_summary" {
  value = organesson_deployment.class_lab.summary
}

output "internet_fedora_summaries" {
  value = {
    for student_name, virtual_machine in organesson_virtual_machine.internet_fedora : student_name => virtual_machine.summary
  }
}

output "charlie_internet_fedora_proxmox_vmid" {
  value = organesson_virtual_machine.internet_fedora["charlie"].proxmox_vmid
}

output "charlie_internet_fedora_proxmox_node" {
  value = organesson_virtual_machine.internet_fedora["charlie"].proxmox_node
}
