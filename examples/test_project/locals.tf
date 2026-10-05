# Alice teaches the class, Bob is the TA, and Charlie and Dave each receive
# an isolated lab. The @organesson identities are test identities provided by
# the application; this project defines groups, ownership, and fixed grants.
locals {
    students = toset(["charlie", "dave"])
    vm_roles = toset(["f1", "f2", "f3"])

    student_machines = {
        for assignment in setproduct(local.students, local.vm_roles) : "${assignment[0]}-${assignment[1]}" => {
            student = assignment[0]
            role    = assignment[1]
        }
    }

    private_static_hosts = {
        f1 = 20
        f2 = 21
        f3 = 22
    }

    managed_networks = merge({
        shared = {
            logical_group_id               = null
            name                           = "shared"
            subnet                         = "192.168.1.0/24"
            gateway                        = "192.168.1.1"
            dhcp_start                     = var.shared_router_dhcp_range.start
            dhcp_end                       = var.shared_router_dhcp_range.end
            dns_servers                    = var.router_lan_dns_servers
            egress_enabled                 = var.shared_router_egress_enabled
            egress_environment_network     = var.shared_router_egress_enabled ? var.cyber_environment_network : null
            egress_address_pool_request_id = var.shared_router_egress_enabled && var.router_egress_ipv4_method == "static" ? organesson_address_pool_request.cyber_ipv4["class"].id : null
            egress_ipv4_method             = var.router_egress_ipv4_method
        }
        }, {
        for student in local.students : "${student}-private" => {
            logical_group_id               = organesson_logical_group.student_lab[student].id
            name                           = "${student}-private"
            subnet                         = "192.168.2.0/24"
            gateway                        = "192.168.2.1"
            dhcp_start                     = var.private_router_dhcp_range.start
            dhcp_end                       = var.private_router_dhcp_range.end
            dns_servers                    = var.private_router_dns_servers
            egress_enabled                 = false
            egress_environment_network     = null
            egress_address_pool_request_id = null
            egress_ipv4_method             = "dhcp"
        }
    })

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

    network_interfaces = merge(
        {
            for student in local.students : "${student}-f1-cyber" => {
                vm_key               = "${student}-f1"
                name                 = "cyber-lab"
                logical_network_id   = null
                environment_network  = var.cyber_environment_network
                address_pool_id      = organesson_address_pool_request.cyber_ipv4["class"].id
                requested_count      = 1
                address_source       = "pool"
                static_address       = null
                ipv4_method          = "static"
                ipv4_never_default   = false
                use_pool_dns_gateway = true
            }
        },
        {
            for student in local.students : "${student}-f3-shared" => {
                vm_key               = "${student}-f3"
                name                 = "shared"
                logical_network_id   = organesson_managed_network.managed["shared"].id
                environment_network  = null
                address_pool_id      = null
                requested_count      = null
                address_source       = "none"
                static_address       = null
                ipv4_method          = "dhcp"
                ipv4_never_default   = false
                use_pool_dns_gateway = false
            }
        },
        {
            for machine_name, machine in local.student_machines : "${machine_name}-private-reserved" => {
                vm_key               = machine_name
                name                 = "private-reserved"
                logical_network_id   = organesson_managed_network.managed["${machine.student}-private"].id
                environment_network  = null
                address_pool_id      = organesson_address_pool_request.private_ipv4[machine.student].id
                requested_count      = 1
                address_source       = "pool"
                static_address       = null
                ipv4_method          = "static"
                ipv4_never_default   = true
                use_pool_dns_gateway = false
            }
        },
        {
            for machine_name, machine in local.student_machines : "${machine_name}-private-manual" => {
                vm_key               = machine_name
                name                 = "private-manual"
                logical_network_id   = organesson_managed_network.managed["${machine.student}-private"].id
                environment_network  = null
                address_pool_id      = null
                requested_count      = null
                address_source       = "manual"
                static_address       = "${cidrhost("192.168.2.0/24", local.private_static_hosts[machine.role])}/24"
                ipv4_method          = "static"
                ipv4_never_default   = true
                use_pool_dns_gateway = false
            }
        },
        {
            for machine_name, machine in local.student_machines : "${machine_name}-private-dhcp" => {
                vm_key               = machine_name
                name                 = "private-dhcp"
                logical_network_id   = organesson_managed_network.managed["${machine.student}-private"].id
                environment_network  = null
                address_pool_id      = null
                requested_count      = null
                address_source       = "none"
                static_address       = null
                ipv4_method          = "dhcp"
                ipv4_never_default   = true
                use_pool_dns_gateway = false
            }
        },
        {
            for student in local.students : "${student}-f1-f3-link" => {
                vm_key               = "${student}-f1"
                name                 = "f1-f3-link"
                logical_network_id   = organesson_unmanaged_network.f1_f3_link[student].id
                environment_network  = null
                address_pool_id      = null
                requested_count      = null
                address_source       = "manual"
                static_address       = "192.168.3.1/24"
                ipv4_method          = "static"
                ipv4_never_default   = true
                use_pool_dns_gateway = false
            }
        },
        {
            for student in local.students : "${student}-f3-f1-link" => {
                vm_key               = "${student}-f3"
                name                 = "f1-f3-link"
                logical_network_id   = organesson_unmanaged_network.f1_f3_link[student].id
                environment_network  = null
                address_pool_id      = null
                requested_count      = null
                address_source       = "manual"
                static_address       = "192.168.3.2/24"
                ipv4_method          = "static"
                ipv4_never_default   = true
                use_pool_dns_gateway = false
            }
        }
    )
}
