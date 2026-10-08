resource "organesson_deployment" "workstation" {
  environment = "cyber-lab-pve"
  name        = "cyber-workstation"
  owner       = "system:platform-administrators"
}

resource "organesson_logical_group" "workstation" {
  deployment_id = organesson_deployment.workstation.id
  name          = "workstation"
}

resource "organesson_user_group" "operators" {
  deployment_id = organesson_deployment.workstation.id
  name          = "cyber-workstation-operators"
  members       = toset(["kgb1043@cyber", "dpm1072@cyber", "jiu8@cyber"])
}
