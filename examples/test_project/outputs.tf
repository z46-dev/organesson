output "deployment_summary" {
  value = organesson_deployment.class_lab.summary
}

output "internet_fedora_summaries" {
  value = {
    for student_name, virtual_machine in organesson_virtual_machine.internet_fedora : student_name => virtual_machine.summary
  }
}
