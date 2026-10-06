resource "organesson_virtual_machine" "student" {
  for_each = local.student_machines

  boot_disk_gib     = 32
  cpu_cores         = 2
  logical_group_id  = organesson_logical_group.student_lab[each.value.student].id
  memory_mib        = 4096
  name              = each.value.role
  pool              = "organesson"
  provisioning_mode = "proxmox"
  storage           = "laas"
  template          = "og-template-fedora-server-latest"
}

resource "organesson_artifact" "first_time_setup" {
  deployment_id    = organesson_deployment.class_lab.id
  entrypoint       = "entrypoint.sh"
  source_directory = "artifacts/first-time-setup"
}

resource "organesson_guest_setup" "student" {
  for_each = local.student_machines

  artifact_id        = organesson_artifact.first_time_setup.id
  entrypoint         = organesson_artifact.first_time_setup.entrypoint
  sha256             = organesson_artifact.first_time_setup.sha256
  source_directory   = organesson_artifact.first_time_setup.source_directory
  virtual_machine_id = organesson_virtual_machine.student[each.key].id
}
