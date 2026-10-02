resource "organesson_guest_network_configuration" "internet_fedora_default" {
  for_each = local.proxmox_internet_vms

  ipv4_address          = "${organesson_network_attachment.internet_fedora_default[each.key].addresses[0]}/${split("/", organesson_network_attachment.internet_fedora_default[each.key].address_prefix)[1]}"
  ipv4_dns              = organesson_network_attachment.internet_fedora_default[each.key].address_dns
  ipv4_gateway          = organesson_network_attachment.internet_fedora_default[each.key].address_gateway
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.internet_fedora_default[each.key].id
}

resource "organesson_guest_network_configuration" "internet_fedora_private_link" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  ipv4_address          = "192.168.50.1/30"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.internet_fedora_private_link[each.key].id
}

resource "organesson_guest_network_configuration" "lan_fedora_private_link" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  ipv4_address          = "192.168.50.2/30"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.lan_fedora_private_link[each.key].id
}

resource "organesson_guest_network_configuration" "lan_fedora_shared_lan" {
  for_each = var.proxmox_test_deployment ? { for student in local.students : student => student } : {}

  ipv4_method           = "dhcp"
  network_attachment_id = organesson_network_attachment.lan_fedora_shared_lan[each.key].id
}
