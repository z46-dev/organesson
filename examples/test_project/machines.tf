# Enable one real VM lifecycle check without cloning the entire classroom topology.
resource "organesson_virtual_machine" "internet_fedora" {
  for_each = local.students

  boot_disk_gib     = 64
  cpu_cores         = 2
  logical_group_id  = organesson_logical_group.student_lab[each.key].id
  memory_mib        = 4096
  name              = "internet-fedora"
  pool              = "organesson"
  provisioning_mode = var.proxmox_test_deployment || (var.proxmox_lifecycle_smoke && each.key == "charlie") ? "proxmox" : "simulated"
  storage           = "laas"
  template          = "og-template-fedora-server-latest"
}

# This Fedora VM joins both its student's private link and the shared DHCP subnet.
resource "organesson_virtual_machine" "lan_fedora" {
  for_each = local.students

  boot_disk_gib     = 64
  cpu_cores         = 2
  logical_group_id  = organesson_logical_group.student_lab[each.key].id
  memory_mib        = 4096
  name              = "lan-fedora"
  provisioning_mode = var.proxmox_test_deployment ? "proxmox" : "simulated"
  template          = "og-template-fedora-server-latest"
}

resource "organesson_virtual_disk" "internet_fedora_data" {
  for_each = local.students

  name               = "data"
  size_gib           = 500
  storage_class      = "fast"
  virtual_machine_id = organesson_virtual_machine.internet_fedora[each.key].id
}

resource "organesson_artifact" "first_time_setup" {
  deployment_id    = organesson_deployment.class_lab.id
  entrypoint       = "entrypoint.sh"
  source_directory = "artifacts/first-time-setup"
}

resource "organesson_guest_setup" "internet_fedora" {
  for_each = local.proxmox_internet_vms

  artifact_id        = organesson_artifact.first_time_setup.id
  entrypoint         = organesson_artifact.first_time_setup.entrypoint
  sha256             = organesson_artifact.first_time_setup.sha256
  source_directory   = organesson_artifact.first_time_setup.source_directory
  virtual_machine_id = organesson_virtual_machine.internet_fedora[each.key].id
}

resource "organesson_guest_setup" "lan_fedora" {
  for_each = var.proxmox_test_deployment ? local.students : toset([])

  artifact_id        = organesson_artifact.first_time_setup.id
  entrypoint         = organesson_artifact.first_time_setup.entrypoint
  sha256             = organesson_artifact.first_time_setup.sha256
  source_directory   = organesson_artifact.first_time_setup.source_directory
  virtual_machine_id = organesson_virtual_machine.lan_fedora[each.key].id
}
