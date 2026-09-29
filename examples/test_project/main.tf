terraform {
  required_providers {
    organesson = {
      source  = "tofu.organesson.dev/organesson/organesson"
      version = "0.1.0"
    }
  }
}

locals {
  teams = toset(["team-1", "team-2", "team-3"])
}

resource "organesson_deployment" "development" {
  environment = "cyber-lab-pve"
  name        = "team-lan-test"
  owner       = "group:cyber-club"
}

resource "organesson_logical_group" "team" {
  for_each = local.teams

  deployment_id = organesson_deployment.development.id
  name          = each.value
  owner         = "group:${each.value}"
}

resource "organesson_network" "lan" {
  for_each = local.teams

  logical_group_id = organesson_logical_group.team[each.key].id
  mode             = "unmanaged-layer-2"
  name             = "LAN"
}

resource "organesson_virtual_machine" "primary" {
  for_each = local.teams

  cpu_cores        = 4
  disk_gib         = 128
  logical_group_id = organesson_logical_group.team[each.key].id
  memory_mib       = 8192
  name             = "primary"
  template         = "og-template-fedora-workstation-latest"
}

resource "organesson_virtual_machine" "secondary" {
  for_each = local.teams

  cpu_cores        = 4
  disk_gib         = 128
  logical_group_id = organesson_logical_group.team[each.key].id
  memory_mib       = 8192
  name             = "secondary"
  template         = "og-template-fedora-workstation-latest"
}

resource "organesson_network_attachment" "primary_default" {
  for_each = local.teams

  environment_network = "cyber.lab"
  name                = "default"
  virtual_machine_id  = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_network_attachment" "primary_lan" {
  for_each = local.teams

  logical_network_id = organesson_network.lan[each.key].id
  name               = "lan"
  static_ipv4        = "192.168.50.1/30"
  virtual_machine_id = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_network_attachment" "secondary_lan" {
  for_each = local.teams

  logical_network_id = organesson_network.lan[each.key].id
  name               = "lan"
  static_ipv4        = "192.168.50.2/30"
  virtual_machine_id = organesson_virtual_machine.secondary[each.key].id
}

resource "organesson_address_reservation" "primary_default" {
  for_each = local.teams

  address_family        = "ipv4"
  environment_network   = "cyber.lab"
  network_attachment_id = organesson_network_attachment.primary_default[each.key].id
}

resource "organesson_artifact" "first_time_setup" {
  deployment_id = organesson_deployment.development.id
  path          = "artifacts/first-time-setup.sh"
  sha256        = "e8bc34322b1b4aa7672f542291fc60ccdd5a6537933402bb6c533d725cd99e14"
}

resource "organesson_guest_setup" "primary" {
  for_each = local.teams

  artifact_id        = organesson_artifact.first_time_setup.id
  virtual_machine_id = organesson_virtual_machine.primary[each.key].id
}

resource "organesson_guest_setup" "secondary" {
  for_each = local.teams

  artifact_id        = organesson_artifact.first_time_setup.id
  virtual_machine_id = organesson_virtual_machine.secondary[each.key].id
}

output "deployment_summary" {
  value = organesson_deployment.development.summary
}

output "primary_vm_summaries" {
  value = {
    for team_name, virtual_machine in organesson_virtual_machine.primary : team_name => virtual_machine.summary
  }
}
