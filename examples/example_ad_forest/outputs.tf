output "domain_name" {
  value = "adforest.test"
}

output "domain_controller" {
  value = {
    vmid    = organesson_virtual_machine.domain_controller.proxmox_vmid
    address = organesson_address_pool_request.domain_controller.addresses[0]
  }
}

output "workstation" {
  value = {
    vmid = organesson_virtual_machine.workstation.proxmox_vmid
    name = "OG-WIN11-01"
  }
}
