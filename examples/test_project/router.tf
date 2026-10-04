resource "organesson_router" "shared" {
    count = var.proxmox_test_deployment ? 1 : 0

    dhcp_end             = var.shared_router_dhcp_range.end
    dhcp_start           = var.shared_router_dhcp_range.start
    dns_servers          = toset(var.router_lan_dns_servers)
    egress_attachment_id = var.shared_router_egress_enabled ? organesson_network_attachment.shared_router_cyber[0].id : null
    lan_attachment_id    = organesson_network_attachment.shared_router_lan[0].id
    lan_ipv4_address     = "192.168.1.1/24"
    subnet               = "192.168.1.0/24"
    virtual_machine_id   = organesson_virtual_machine.shared_router[0].id

    depends_on = [
        organesson_guest_network_configuration.shared_router_lan,
        organesson_guest_network_configuration.shared_router_cyber
    ]
}

resource "organesson_router" "private" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    dhcp_end           = var.private_router_dhcp_range.end
    dhcp_start         = var.private_router_dhcp_range.start
    dns_servers        = toset(var.private_router_dns_servers)
    lan_attachment_id  = organesson_network_attachment.private_router_lan[each.key].id
    lan_ipv4_address   = local.private_networks[each.key].gateway
    subnet             = local.private_networks[each.key].subnet
    virtual_machine_id = organesson_virtual_machine.private_router[each.key].id

    depends_on = [organesson_guest_network_configuration.private_router_lan]
}
