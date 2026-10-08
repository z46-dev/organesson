resource "organesson_virtual_machine" "workstation" {
  boot_disk_gib     = 32
  cpu_cores         = 8
  memory_mib        = 16384
  logical_group_id  = organesson_logical_group.workstation.id
  name              = "fedora-workstation"
  pool              = "organesson"
  provisioning_mode = "proxmox"
  storage           = "laas"
  template          = "og-template-fedora-workstation-latest"
}

resource "organesson_network_attachment" "cyber" {
  address_pool_request_id      = organesson_address_pool_request.cyber_ipv4.id
  environment_network          = "cyber"
  ipv6_address_pool_request_id = organesson_address_pool_request.cyber_ipv6.id
  name                         = "cyber"
  requested_address_count      = 1
  requested_ipv6_address_count = 1
  virtual_machine_id           = organesson_virtual_machine.workstation.id
}

resource "organesson_guest_network_configuration" "cyber" {
  ipv4_address          = format("%s/%s", organesson_address_pool_request.cyber_ipv4.addresses[0], split("/", organesson_address_pool_request.cyber_ipv4.prefix)[1])
  ipv4_dns              = organesson_address_pool_request.cyber_ipv4.dns
  ipv4_gateway          = organesson_address_pool_request.cyber_ipv4.gateway
  ipv4_method           = "static"
  ipv6_address          = format("%s/%s", organesson_address_pool_request.cyber_ipv6.addresses[0], split("/", organesson_address_pool_request.cyber_ipv6.prefix)[1])
  ipv6_dns              = organesson_address_pool_request.cyber_ipv6.dns
  ipv6_gateway          = organesson_address_pool_request.cyber_ipv6.gateway
  ipv6_method           = "static"
  network_attachment_id = organesson_network_attachment.cyber.id
}

resource "organesson_artifact" "workstation_setup" {
  deployment_id    = organesson_deployment.workstation.id
  entrypoint       = "setup.sh"
  source_directory = "artifacts"
}

resource "organesson_guest_setup" "workstation" {
  artifact_id        = organesson_artifact.workstation_setup.id
  entrypoint         = organesson_artifact.workstation_setup.entrypoint
  sha256             = organesson_artifact.workstation_setup.sha256
  source_directory   = organesson_artifact.workstation_setup.source_directory
  virtual_machine_id = organesson_virtual_machine.workstation.id
}

resource "organesson_permission_grant" "operators" {
  for_each   = toset(["resource.view", "vm.snapshot_control", "vm.console_control", "vm.power_control"])
  permission = each.value
  scope      = "self"
  subject_id = organesson_user_group.operators.id
  target_id  = tostring(organesson_virtual_machine.workstation.ownership_node_id)
}
