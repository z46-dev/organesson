resource "organesson_virtual_machine" "fedora" {
  boot_disk_gib     = 64
  cpu_cores         = 2
  logical_group_id  = organesson_logical_group.charlie_lab.id
  memory_mib        = 4096
  name              = "charlie-fedora-lifecycle"
  pool              = "organesson"
  provisioning_mode = "proxmox"
  storage           = "laas"
  template          = "og-template-fedora-server-latest"
}

resource "organesson_permission_grant" "charlie_view" {
  permission = "resource.view"
  scope      = "self"
  subject_id = organesson_user_group.charlie.id
  target_id  = tostring(organesson_virtual_machine.fedora.ownership_node_id)
}

resource "organesson_permission_grant" "charlie_power" {
  permission = "vm.power_control"
  scope      = "self"
  subject_id = organesson_user_group.charlie.id
  target_id  = tostring(organesson_virtual_machine.fedora.ownership_node_id)
}
