output "deployment_summary" {
  value = organesson_deployment.class_lab.summary
}

output "student_vm_summaries" {
  value = {
    for machine_name, virtual_machine in organesson_virtual_machine.student : machine_name => virtual_machine.summary
  }
}

output "charlie_f1_proxmox_vmid" {
  value = organesson_virtual_machine.student["charlie-f1"].proxmox_vmid
}

output "charlie_f1_proxmox_node" {
  value = organesson_virtual_machine.student["charlie-f1"].proxmox_node
}
