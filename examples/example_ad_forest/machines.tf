resource "organesson_virtual_machine" "domain_controller" {
  boot_disk_gib     = 128
  cpu_cores         = 4
  logical_group_id  = organesson_logical_group.forest.id
  memory_mib        = 8192
  name              = "domain-controller"
  pool              = "organesson"
  provisioning_mode = "proxmox"
  storage           = "laas"
  template          = "og-template-windows-server-2025"
}

resource "organesson_virtual_machine" "workstation" {
  boot_disk_gib     = 128
  cpu_cores         = 4
  logical_group_id  = organesson_logical_group.forest.id
  memory_mib        = 8192
  name              = "windows-11-workstation"
  pool              = "organesson"
  provisioning_mode = "proxmox"
  storage           = "laas"
  template          = "og-template-windows-11-pro-workstation"
}

resource "organesson_network_attachment" "domain_controller" {
  address_pool_request_id = organesson_address_pool_request.domain_controller.id
  logical_network_id      = organesson_managed_network.forest.id
  model                   = "e1000e"
  name                    = "ad-lan"
  requested_address_count = 1
  virtual_machine_id      = organesson_virtual_machine.domain_controller.id
}

resource "organesson_network_attachment" "workstation" {
  logical_network_id = organesson_managed_network.forest.id
  model              = "e1000e"
  name               = "ad-lan"
  virtual_machine_id = organesson_virtual_machine.workstation.id
}
