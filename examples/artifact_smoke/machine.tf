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

resource "organesson_artifact" "smoke" {
  deployment_id    = organesson_deployment.lifecycle.id
  entrypoint       = "entrypoint.sh"
  source_directory = "artifacts/smoke"
}

resource "organesson_guest_setup" "smoke" {
  artifact_id        = organesson_artifact.smoke.id
  entrypoint         = organesson_artifact.smoke.entrypoint
  sha256             = organesson_artifact.smoke.sha256
  source_directory   = organesson_artifact.smoke.source_directory
  virtual_machine_id = organesson_virtual_machine.fedora.id
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

resource "organesson_permission_grant" "charlie_console" {
  permission = "vm.console_control"
  scope      = "self"
  subject_id = organesson_user_group.charlie.id
  target_id  = tostring(organesson_virtual_machine.fedora.ownership_node_id)
}

resource "organesson_permission_grant" "charlie_snapshot" {
  permission = "vm.snapshot_control"
  scope      = "self"
  subject_id = organesson_user_group.charlie.id
  target_id  = tostring(organesson_virtual_machine.fedora.ownership_node_id)
}
