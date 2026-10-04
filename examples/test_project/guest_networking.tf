resource "organesson_guest_network_configuration" "student_f1_cyber" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    ipv4_address          = "${organesson_network_attachment.student_f1_cyber[each.key].addresses[0]}/${split("/", organesson_network_attachment.student_f1_cyber[each.key].address_prefix)[1]}"
    ipv4_dns              = organesson_network_attachment.student_f1_cyber[each.key].address_dns
    ipv4_gateway          = organesson_network_attachment.student_f1_cyber[each.key].address_gateway
    ipv4_method           = "static"
    network_attachment_id = organesson_network_attachment.student_f1_cyber[each.key].id
}

resource "organesson_guest_network_configuration" "shared_router_lan" {
    count = var.proxmox_test_deployment ? 1 : 0

    ipv4_address          = "192.168.1.1/24"
    ipv4_method           = "static"
    ipv4_never_default    = true
    network_attachment_id = organesson_network_attachment.shared_router_lan[0].id
}

resource "organesson_guest_network_configuration" "shared_router_cyber" {
    count = var.proxmox_test_deployment && var.shared_router_egress_enabled ? 1 : 0

    ipv4_address = var.router_egress_ipv4_method == "static" ? "${organesson_network_attachment.shared_router_cyber[0].addresses[0]}/${split("/", organesson_network_attachment.shared_router_cyber[0].address_prefix)[1]}" : null
    ipv4_dns     = var.router_egress_ipv4_method == "static" ? organesson_network_attachment.shared_router_cyber[0].address_dns : null
    ipv4_gateway = var.router_egress_ipv4_method == "static" ? organesson_network_attachment.shared_router_cyber[0].address_gateway : null
    ipv4_method  = var.router_egress_ipv4_method
    network_attachment_id = organesson_network_attachment.shared_router_cyber[0].id
}

resource "organesson_guest_network_configuration" "private_router_lan" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    ipv4_address          = local.private_networks[each.key].gateway
    ipv4_method           = "static"
    ipv4_never_default    = true
    network_attachment_id = organesson_network_attachment.private_router_lan[each.key].id
}

resource "organesson_guest_network_configuration" "student_private_reserved" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    ipv4_address          = "${organesson_network_attachment.student_private_reserved[each.key].addresses[0]}/${split("/", organesson_network_attachment.student_private_reserved[each.key].address_prefix)[1]}"
    ipv4_method           = "static"
    ipv4_never_default    = true
    network_attachment_id = organesson_network_attachment.student_private_reserved[each.key].id
}

resource "organesson_guest_network_configuration" "student_private_manual" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    ipv4_address = format(
        "%s/24",
        cidrhost(local.private_networks[each.value.student].subnet, local.private_static_hosts[each.value.role])
    )
    ipv4_method           = "static"
    ipv4_never_default    = true
    network_attachment_id = organesson_network_attachment.student_private_manual[each.key].id
}

resource "organesson_guest_network_configuration" "student_private_dhcp" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    ipv4_method           = "dhcp"
    ipv4_never_default    = true
    network_attachment_id = organesson_network_attachment.student_private_dhcp[each.key].id
}

resource "organesson_guest_network_configuration" "student_f3_shared" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    ipv4_method           = "dhcp"
    network_attachment_id = organesson_network_attachment.student_f3_shared[each.key].id
}

resource "organesson_guest_network_configuration" "student_f1_f3_link" {
    for_each = var.proxmox_test_deployment ? { for key, machine in local.student_machines : key => machine if machine.role != "f2" } : {}

    ipv4_address = each.value.role == "f1" ? "192.168.3.1/24" : "192.168.3.2/24"
    ipv4_method  = "static"

    network_attachment_id = organesson_network_attachment.student_f1_f3_link[each.key].id
}
