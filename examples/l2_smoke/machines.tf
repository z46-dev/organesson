resource "organesson_virtual_machine" "node_a" {
  boot_disk_gib     = 32
  cpu_cores         = 1
  logical_group_id  = organesson_logical_group.lab.id
  memory_mib        = 2048
  name              = "l2-smoke-a"
  pool              = "organesson"
  provisioning_mode = var.provisioning_mode
  storage           = "laas"
  template          = "og-template-fedora-server-latest"
}

resource "organesson_virtual_machine" "node_b" {
  boot_disk_gib     = 32
  cpu_cores         = 1
  logical_group_id  = organesson_logical_group.lab.id
  memory_mib        = 2048
  name              = "l2-smoke-b"
  pool              = "organesson"
  provisioning_mode = var.provisioning_mode
  storage           = "laas"
  template          = "og-template-fedora-server-latest"
}
