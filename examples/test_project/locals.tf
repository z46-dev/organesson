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

    private_networks = {
        for student in local.students : student => {
            subnet  = "192.168.2.0/24"
            gateway = "192.168.2.1/24"
        }
    }

    private_static_hosts = {
        f1 = 20
        f2 = 21
        f3 = 22
    }

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
}
