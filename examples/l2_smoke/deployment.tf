resource "organesson_deployment" "l2_smoke" {
  environment = "cyber-lab-pve"
  name        = "vxlan-l2-smoke"
  owner       = "system:platform-administrators"
}

resource "organesson_logical_group" "lab" {
  deployment_id = organesson_deployment.l2_smoke.id
  name          = "l2-smoke"
}
