locals {
    students = toset(["charlie", "dave"])
    student_permissions = toset([
        "resource.view",
        "vm.power_control"
    ])
    student_grants = {
        for assignment in setproduct(local.students, local.student_permissions) : "${assignment[0]}:${assignment[1]}" => {
            student    = assignment[0]
            permission = assignment[1]
        }
    }
}

variable "charlie_members" {
    type    = set(string)
    default = ["charlie@organesson"]
}

resource "organesson_deployment" "class_lab" {
    name        = "provider-acceptance-class-lab"
    description = "Alice, Bob, Charlie, and Dave provider/API acceptance deployment"
}

resource "organesson_user_group" "student" {
    for_each = local.students

    deployment_id = organesson_deployment.class_lab.id
    name          = "${each.value}-access"
    members       = each.value == "charlie" ? var.charlie_members : toset(["${each.value}@organesson"])
}

resource "organesson_logical_group" "student_lab" {
    for_each = local.students

    deployment_id = organesson_deployment.class_lab.id
    name          = "${each.value}-lab"
}

resource "organesson_virtual_machine" "student_fedora" {
    for_each = local.students

    boot_disk_gib    = 64
    cpu_cores        = 2
    logical_group_id = organesson_logical_group.student_lab[each.key].id
    memory_mib       = 4096
    name             = "${each.value}-fedora"
    template          = "og-template-fedora-server-latest"
}

resource "organesson_permission_grant" "student" {
    for_each = local.student_grants

    permission = each.value.permission
    scope      = "descendants"
    subject_id = organesson_user_group.student[each.value.student].id
    target_id  = organesson_logical_group.student_lab[each.value.student].id
}

output "deployment_id" {
    value = organesson_deployment.class_lab.id
}

output "student_vm_ids" {
    value = { for student, vm in organesson_virtual_machine.student_fedora : student => vm.id }
}

output "student_group_ids" {
    value = { for student, group in organesson_user_group.student : student => group.id }
}
