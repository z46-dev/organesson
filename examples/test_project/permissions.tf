resource "organesson_permission_grant" "student" {
    for_each = local.student_permission_grants

    permission = each.value.permission
    scope      = "descendants"
    subject_id = "${each.value.student}@organesson"
    target_id  = organesson_logical_group.student_lab[each.value.student].id
}

resource "organesson_permission_grant" "teaching_staff" {
    for_each = local.teaching_staff_permissions

    permission = each.value
    scope      = "descendants"
    subject_id = organesson_user_group.teaching_staff.id
    target_id  = tostring(organesson_deployment.class_lab.root_node_id)
}

resource "organesson_permission_grant" "alice_deployment" {
    for_each = local.alice_deployment_permissions

    permission = each.value
    scope      = "self"
    subject_id = "alice@organesson"
    target_id  = tostring(organesson_deployment.class_lab.root_node_id)
}
