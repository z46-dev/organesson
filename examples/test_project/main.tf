terraform {
  required_providers {
    organesson = {
      source  = "tofu.organesson.dev/organesson/organesson"
      version = "0.1.0"
    }
  }
}

resource "organesson_team_lan" "development" {
  environment          = "cyber-lab-pve"
  name                 = "team-lan-test"
  owner                = "group:cyber-club"
  teams                = ["team-1", "team-2", "team-3"]
  workstation_template = "og-template-fedora-workstation-latest"

  primary_cpu_cores  = 4
  primary_memory_mib = 8192
  primary_disk_gib   = 128

  secondary_cpu_cores  = 4
  secondary_memory_mib = 8192
  secondary_disk_gib   = 128

  setup_artifact = "artifacts/first-time-setup.sh"
}

output "planned_actions" {
  value = organesson_team_lan.development.planned_actions
}
