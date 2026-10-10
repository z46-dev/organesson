resource "organesson_guest_workflow" "forest" {
  definition_file = "workflow/forest.toml"
  source_root     = "."

  virtual_machines = {
    domain_controller = organesson_virtual_machine.domain_controller.id
    workstation       = organesson_virtual_machine.workstation.id
  }

  depends_on = [
    organesson_network_attachment.domain_controller,
    organesson_network_attachment.workstation
  ]
}
