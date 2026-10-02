resource "organesson_network" "private_link" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}


  dhcp_enabled     = false
  egress_policy    = "isolated"
  logical_group_id = organesson_logical_group.student_lab[each.key].id
  mode             = "unmanaged-layer-2"
  name             = "private-link"
}

# This Proxmox SDN-backed network is created once for the deployment. Every
# lan_fedora joins its subnet, receives DHCP configuration, and has no uplink.
resource "organesson_network" "shared_student_lan" {
  for_each = var.proxmox_test_deployment ? { shared = true } : {}

  deployment_id = organesson_deployment.class_lab.id
  dhcp_enabled  = true
  egress_policy = "isolated"
  ipv4_gateway  = "192.168.100.1"
  ipv4_subnet   = "192.168.100.0/24"
  mode          = "managed"
  name          = "shared-student-lan"
}

# This is the deployment's finite request to cyber.lab. Every interface below
# that consumes one of these addresses references this pool explicitly.
resource "organesson_address_pool_request" "internet_fedora_ipv4" {
  for_each = length(local.proxmox_internet_vms) > 0 ? { classroom = true } : {}

  address_count       = length(local.proxmox_internet_vms)
  address_family      = "ipv4"
  deployment_id       = organesson_deployment.class_lab.id
  environment_network = "cyber.lab"
  name                = "internet-fedora-ipv4"
}

resource "organesson_network_attachment" "internet_fedora_default" {
  for_each = local.proxmox_internet_vms

  address_pool_request_id = organesson_address_pool_request.internet_fedora_ipv4["classroom"].id
  environment_network     = "cyber.lab"
  name                    = "default"
  requested_address_count = 1
  virtual_machine_id      = organesson_virtual_machine.internet_fedora[each.key].id
}

resource "organesson_network_attachment" "internet_fedora_private_link" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  logical_network_id = organesson_network.private_link[each.key].id
  name               = "private-link"
  virtual_machine_id = organesson_virtual_machine.internet_fedora[each.key].id
}

resource "organesson_network_attachment" "lan_fedora_private_link" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  logical_network_id = organesson_network.private_link[each.key].id
  name               = "private-link"
  virtual_machine_id = organesson_virtual_machine.lan_fedora[each.key].id
}

resource "organesson_network_attachment" "lan_fedora_shared_lan" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  logical_network_id = organesson_network.shared_student_lan["shared"].id
  name               = "shared-lan"
  virtual_machine_id = organesson_virtual_machine.lan_fedora[each.key].id
}
