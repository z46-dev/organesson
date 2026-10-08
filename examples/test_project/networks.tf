resource "organesson_address_pool_request" "cyber_ipv4" {
  for_each = { class = true }

  address_count       = length(local.students) + (var.shared_router_egress_enabled ? 1 : 0)
  address_family      = "ipv4"
  deployment_id       = organesson_deployment.class_lab.id
  environment_network = var.cyber_environment_network
  name                = "cyber-lab-static-addresses"
}

resource "organesson_managed_network" "managed" {
  for_each = local.managed_networks

  deployment_id                  = each.value.logical_group_id == null ? organesson_deployment.class_lab.id : null
  logical_group_id               = each.value.logical_group_id
  name                           = each.value.name
  ipv4_subnet                    = each.value.subnet
  ipv4_gateway                   = each.value.gateway
  ipv4_dhcp_enabled              = true
  dhcp_start                     = each.value.dhcp_start
  dhcp_end                       = each.value.dhcp_end
  dns_servers                    = toset(each.value.dns_servers)
  ipv6_subnet                    = each.value.ipv6_subnet
  ipv6_gateway                   = each.value.ipv6_gateway
  ipv6_dhcp_enabled              = each.value.ipv6_dhcp_enabled
  ipv6_dhcp_start                = try(each.value.ipv6_dhcp_start, "")
  ipv6_dhcp_end                  = try(each.value.ipv6_dhcp_end, "")
  egress_enabled                 = each.value.egress_enabled
  egress_environment_network     = each.value.egress_environment_network
  egress_address_pool_request_id = each.value.egress_address_pool_request_id
  egress_ipv4_method             = each.value.egress_ipv4_method

  dynamic "external_vlan" {
    for_each = try(each.value.external_vlan, null) == null ? [] : [each.value.external_vlan]

    content {
      trunk_node   = external_vlan.value.trunk_node
      trunk_bridge = external_vlan.value.trunk_bridge
      vlan_id      = external_vlan.value.vlan_id
    }
  }
}

resource "organesson_unmanaged_network" "f1_f3_link" {
  for_each = local.students

  dhcp_enabled     = false
  egress_policy    = "isolated"
  logical_group_id = organesson_logical_group.student_lab[each.key].id
  name             = "f1-f3-link"
}

resource "organesson_address_pool_request" "private_ipv4" {
  for_each = local.students

  address_count      = 3
  address_family     = "ipv4"
  deployment_id      = organesson_deployment.class_lab.id
  logical_network_id = organesson_managed_network.managed["${each.key}-private"].id
  name               = "${each.key}-private-reserved-addresses"
  range_start        = "192.168.2.2"
  range_end          = "192.168.2.10"
}
