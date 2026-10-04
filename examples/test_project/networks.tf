resource "organesson_address_pool_request" "cyber_ipv4" {
    for_each = var.proxmox_test_deployment ? { class = true } : {}

    address_count       = length(local.students) + (var.shared_router_egress_enabled && var.router_egress_ipv4_method == "static" ? 1 : 0)
    address_family      = "ipv4"
    deployment_id       = organesson_deployment.class_lab.id
    environment_network = var.cyber_environment_network
    name                = "cyber-lab-static-addresses"
}

resource "organesson_network" "shared" {
    count = var.proxmox_test_deployment ? 1 : 0

    deployment_id = organesson_deployment.class_lab.id
    dhcp_enabled  = true
    egress_policy = "isolated"
    ipv4_gateway  = "192.168.1.1"
    ipv4_subnet   = "192.168.1.0/24"
    mode          = "managed"
    name          = "shared"
    router_vmid   = tonumber(organesson_virtual_machine.shared_router[0].proxmox_vmid)
}

resource "organesson_network" "private" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    dhcp_enabled     = true
    egress_policy    = "isolated"
    ipv4_gateway     = "192.168.2.1"
    ipv4_subnet      = local.private_networks[each.key].subnet
    logical_group_id = organesson_logical_group.student_lab[each.key].id
    mode             = "managed"
    name             = "private"
    router_vmid      = tonumber(organesson_virtual_machine.private_router[each.key].proxmox_vmid)
}

resource "organesson_network" "f1_f3_link" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    dhcp_enabled     = false
    egress_policy    = "isolated"
    logical_group_id = organesson_logical_group.student_lab[each.key].id
    mode             = "unmanaged-layer-2"
    name             = "f1-f3-link"
}

resource "organesson_address_pool_request" "private_ipv4" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    address_count     = 3
    address_family    = "ipv4"
    deployment_id     = organesson_deployment.class_lab.id
    logical_network_id = organesson_network.private[each.key].id
    name              = "${each.key}-private-static-addresses"
    range_start       = "192.168.2.2"
    range_end         = "192.168.2.10"
}

resource "organesson_network_attachment" "shared_router_lan" {
    count = var.proxmox_test_deployment ? 1 : 0

    logical_network_id = organesson_network.shared[0].id
    name               = "lan"
    virtual_machine_id = organesson_virtual_machine.shared_router[0].id
}

resource "organesson_network_attachment" "private_router_lan" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    logical_network_id = organesson_network.private[each.key].id
    name               = "lan"
    virtual_machine_id = organesson_virtual_machine.private_router[each.key].id
}

resource "organesson_network_attachment" "shared_router_cyber" {
    count = var.proxmox_test_deployment && var.shared_router_egress_enabled ? 1 : 0

    address_pool_request_id = var.router_egress_ipv4_method == "static" ? organesson_address_pool_request.cyber_ipv4["class"].id : null
    environment_network     = var.cyber_environment_network
    name                    = "egress"
    requested_address_count = var.router_egress_ipv4_method == "static" ? 1 : null
    virtual_machine_id      = organesson_virtual_machine.shared_router[0].id
}

resource "organesson_network_attachment" "student_f1_cyber" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    address_pool_request_id = organesson_address_pool_request.cyber_ipv4["class"].id
    environment_network     = var.cyber_environment_network
    name                    = "cyber-lab"
    requested_address_count = 1
    virtual_machine_id      = organesson_virtual_machine.student["${each.key}-f1"].id
}

resource "organesson_network_attachment" "student_f3_shared" {
    for_each = var.proxmox_test_deployment ? local.students : toset([])

    logical_network_id = organesson_network.shared[0].id
    name               = "shared"
    virtual_machine_id = organesson_virtual_machine.student["${each.key}-f3"].id
}

resource "organesson_network_attachment" "student_private_reserved" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    address_pool_request_id = organesson_address_pool_request.private_ipv4[each.value.student].id
    logical_network_id      = organesson_network.private[each.value.student].id
    name                    = "private-reserved"
    requested_address_count = 1
    virtual_machine_id      = organesson_virtual_machine.student[each.key].id
}

resource "organesson_network_attachment" "student_private_manual" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    logical_network_id = organesson_network.private[each.value.student].id
    name               = "private-manual"
    virtual_machine_id = organesson_virtual_machine.student[each.key].id
}

resource "organesson_network_attachment" "student_private_dhcp" {
    for_each = var.proxmox_test_deployment ? local.student_machines : {}

    logical_network_id = organesson_network.private[each.value.student].id
    name               = "private-dhcp"
    virtual_machine_id = organesson_virtual_machine.student[each.key].id
}

resource "organesson_network_attachment" "student_f1_f3_link" {
    for_each = var.proxmox_test_deployment ? { for key, machine in local.student_machines : key => machine if machine.role != "f2" } : {}

    logical_network_id = organesson_network.f1_f3_link[each.value.student].id
    name               = "f1-f3-link"
    virtual_machine_id = organesson_virtual_machine.student[each.key].id
}
