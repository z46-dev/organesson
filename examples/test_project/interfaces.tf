resource "organesson_network_attachment" "interface" {
  for_each = local.network_interfaces

  address_pool_request_id      = each.value.address_pool_id
  environment_network          = each.value.environment_network
  logical_network_id           = each.value.logical_network_id
  name                         = each.value.name
  requested_address_count      = each.value.requested_count
  ipv6_address_pool_request_id = each.value.ipv6_address_pool_id
  requested_ipv6_address_count = each.value.requested_ipv6_count
  virtual_machine_id           = organesson_virtual_machine.student[each.value.vm_key].id
}

resource "organesson_guest_network_configuration" "interface" {
  for_each = local.network_interfaces

  ipv4_address = each.value.address_source == "pool" ? format(
    "%s/%s",
    organesson_network_attachment.interface[each.key].addresses[0],
    split("/", organesson_network_attachment.interface[each.key].address_prefix)[1]
  ) : each.value.address_source == "manual" ? each.value.static_address : null
  ipv4_dns              = each.value.use_pool_dns_gateway ? organesson_network_attachment.interface[each.key].address_dns : each.value.ipv4_dns
  ipv4_gateway          = each.value.use_pool_dns_gateway ? organesson_network_attachment.interface[each.key].address_gateway : each.value.ipv4_gateway
  ipv4_method           = each.value.ipv4_method
  ipv4_never_default    = each.value.ipv4_never_default
  ipv6_address          = each.value.ipv6_address
  ipv6_dns              = each.value.ipv6_dns
  ipv6_gateway          = each.value.ipv6_gateway
  ipv6_method           = each.value.ipv6_method
  ipv6_never_default    = true
  network_attachment_id = organesson_network_attachment.interface[each.key].id
}
