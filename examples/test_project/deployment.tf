resource "organesson_deployment" "class_lab" {
  environment = "cyber-lab-pve"
  name        = "alice-class-lab"
  owner       = "system:platform-administrators"
}

# Each student's group is the sole owner of the student's logical lab root.
resource "organesson_user_group" "student" {
  for_each = local.students

  deployment_id = organesson_deployment.class_lab.id
  name          = each.value
  members       = toset(["${each.value}@organesson"])
}

resource "organesson_user_group" "students" {
  deployment_id = organesson_deployment.class_lab.id
  name          = "students"
  members       = toset(["charlie@organesson", "dave@organesson"])
}

resource "organesson_user_group" "teaching_staff" {
  deployment_id = organesson_deployment.class_lab.id
  name          = "teaching-staff"
  members       = toset(["alice@organesson", "bob@organesson"])
}

resource "organesson_logical_group" "student_lab" {
  for_each = local.students

  deployment_id = organesson_deployment.class_lab.id
  name          = "${each.value}-lab"
  owner         = organesson_user_group.student[each.key].id
}
