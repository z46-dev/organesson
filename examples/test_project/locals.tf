# Alice teaches the class, Bob is the TA, and Charlie and Dave each receive
# an isolated lab. The @organesson identities are resolved by Organesson's
# future built-in test identity source, not created by this configuration.
locals {
  students = toset(["charlie", "dave"])

  proxmox_internet_vms = {
    for student in local.students : student => student
    if var.proxmox_test_deployment || (var.proxmox_lifecycle_smoke && student == "charlie")
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
