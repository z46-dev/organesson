terraform {
  required_providers {
    organesson = {
      source  = "tofu.organesson.dev/organesson/organesson"
      version = "0.1.0"
    }
  }
}

locals {
  students = toset(["charlie", "dave"])

  student_permissions = toset([
    "resource.view",
    "vm.console_control",
    "vm.power_control"
  ])

  teaching_staff_permissions = toset([
    "resource.view",
    "vm.console_control",
    "vm.power_control",
    "vm.snapshot_control"
  ])

  alice_deployment_permissions = toset([
    "deployment.manage_configuration",
    "deployment.manage_groups",
    "deployment.manage_permissions",
    "deployment.manage_users"
  ])

  student_permission_grants = {
    for assignment in setproduct(local.students, local.student_permissions) : "${assignment[0]}:${assignment[1]}" => {
      permission = assignment[1]
      student    = assignment[0]
    }
  }
}

resource "organesson_deployment" "development" {
  environment = "cyber-lab-pve"
  name        = "team-lan-test"
  owner       = "group:cyber-club"
}

resource "organesson_logical_group" "student" {
  for_each = local.students

  deployment_id = organesson_deployment.development.id
  name          = each.value
  owner         = "group:${each.value}"
}

resource "organesson_user_group" "students" {
  deployment_id = organesson_deployment.development.id
  name          = "students"

  members = toset([
    "charlie@organesson",
    "dave@organesson"
  ])
}

resource "organesson_user_group" "teaching_staff" {
  deployment_id = organesson_deployment.development.id
  name          = "teaching-staff"

  members = toset([
    "alice@organesson",
    "bob@organesson"
  ])
}

resource "organesson_network" "lan" {
  for_each = local.students

  logical_group_id = organesson_logical_group.student[each.key].id
  mode             = "unmanaged-layer-2"
  name             = "LAN"
}

resource "organesson_virtual_machine" "primary" {
  for_each = local.students

  boot_disk_gib    = 128
  cpu_cores        = 4
  logical_group_id = organesson_logical_group.student[each.key].id
  memory_mib       = 8192
  name             = "primary"
  template         = "og-template-fedora-workstation-latest"
}

resource "organesson_virtual_machine" "secondary" {
  for_each = local.students

  boot_disk_gib    = 128
  cpu_cores        = 4
  logical_group_id = organesson_logical_group.student[each.key].id
  memory_mib       = 8192
  name             = "secondary"
  template         = "og-template-fedora-workstation-latest"
}

resource "organesson_virtual_disk" "primary_data" {
  for_each = local.students

  name               = "data"
  size_gib           = 500
  storage_class      = "fast"
  virtual_machine_id = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_network_attachment" "primary_default" {
  for_each = local.students

  environment_network = "cyber.lab"
  name                = "default"
  virtual_machine_id  = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_network_attachment" "primary_lan" {
  for_each = local.students

  logical_network_id = organesson_network.lan[each.key].id
  name               = "lan"
  virtual_machine_id = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_network_attachment" "secondary_lan" {
  for_each = local.students

  logical_network_id = organesson_network.lan[each.key].id
  name               = "lan"
  virtual_machine_id = organesson_virtual_machine.secondary[each.key].id
}

resource "organesson_guest_network_configuration" "primary_lan" {
  for_each = local.students

  ipv4_address          = "192.168.50.1/30"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.primary_lan[each.key].id
}

resource "organesson_guest_network_configuration" "secondary_lan" {
  for_each = local.students

  ipv4_address          = "192.168.50.2/30"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.secondary_lan[each.key].id
}

resource "organesson_address_reservation" "primary_default" {
  for_each = local.students

  address_family        = "ipv4"
  environment_network   = "cyber.lab"
  network_attachment_id = organesson_network_attachment.primary_default[each.key].id
}

resource "organesson_artifact" "first_time_setup" {
  deployment_id    = organesson_deployment.development.id
  entrypoint       = "entrypoint.sh"
  source_directory = "artifacts/first-time-setup"
}

resource "organesson_guest_setup" "primary" {
  for_each = local.students

  artifact_id        = organesson_artifact.first_time_setup.id
  virtual_machine_id = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_guest_setup" "secondary" {
  for_each = local.students

  artifact_id        = organesson_artifact.first_time_setup.id
  virtual_machine_id = organesson_virtual_machine.secondary[each.key].id
}

resource "organesson_permission_grant" "student" {
  for_each = local.student_permission_grants

  permission = each.value.permission
  scope      = "descendants"
  subject_id = "${each.value.student}@organesson"
  target_id  = organesson_logical_group.student[each.value.student].id
}

resource "organesson_permission_grant" "teaching_staff" {
  for_each = local.teaching_staff_permissions

  permission = each.value
  scope      = "descendants"
  subject_id = organesson_user_group.teaching_staff.id
  target_id  = organesson_deployment.development.id
}

resource "organesson_permission_grant" "alice_deployment" {
  for_each = local.alice_deployment_permissions

  permission = each.value
  scope      = "self"
  subject_id = "alice@organesson"
  target_id  = organesson_deployment.development.id
}

output "deployment_summary" {
  value = organesson_deployment.development.summary
}

output "primary_vm_summaries" {
  value = {
    for student_name, virtual_machine in organesson_virtual_machine.primary : student_name => virtual_machine.summary
  }
}
