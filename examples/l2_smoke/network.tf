resource "organesson_network" "l2" {
  deployment_id = organesson_deployment.l2_smoke.id
  dhcp_enabled  = false
  egress_policy = "isolated"
  mode          = "unmanaged-layer-2"
  name          = "vxlan-l2"
}

resource "organesson_network_attachment" "node_a" {
  logical_network_id = organesson_network.l2.id
  name               = "l2"
  virtual_machine_id = organesson_virtual_machine.node_a.id
}

resource "organesson_network_attachment" "node_b" {
  logical_network_id = organesson_network.l2.id
  name               = "l2"
  virtual_machine_id = organesson_virtual_machine.node_b.id
}

resource "organesson_guest_network_configuration" "node_a" {
  ipv4_address          = "192.168.250.11/24"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.node_a.id
}

resource "organesson_guest_network_configuration" "node_b" {
  ipv4_address          = "192.168.250.12/24"
  ipv4_method           = "static"
  network_attachment_id = organesson_network_attachment.node_b.id
}
