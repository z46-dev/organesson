resource "organesson_deployment" "lifecycle" {
  name        = "proxmox-vm-lifecycle"
  description = "Single-VM Proxmox lifecycle acceptance run"
}

resource "organesson_logical_group" "charlie_lab" {
  deployment_id = organesson_deployment.lifecycle.id
  name          = "charlie-lab"
}

resource "organesson_user_group" "charlie" {
  deployment_id = organesson_deployment.lifecycle.id
  name          = "charlie-access"
  members       = toset(["charlie@organesson"])
}
