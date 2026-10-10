resource "organesson_managed_network" "forest" {
  deployment_id                  = organesson_deployment.forest.id
  name                           = "ad-forest-lan"
  ipv4_subnet                    = "192.168.77.0/24"
  ipv4_gateway                   = "192.168.77.1"
  ipv4_dhcp_enabled              = true
  dhcp_start                     = "192.168.77.30"
  dhcp_end                       = "192.168.77.40"
  dns_servers                    = ["192.168.77.10"]
  egress_enabled                 = true
  egress_environment_network     = "cyber"
  egress_address_pool_request_id = organesson_address_pool_request.router_egress.id
  egress_ipv4_method             = "static"
}

resource "organesson_address_pool_request" "router_egress" {
  address_count       = 1
  address_family      = "ipv4"
  deployment_id       = organesson_deployment.forest.id
  environment_network = "cyber"
  name                = "ad-forest-router-egress"
}

resource "organesson_address_pool_request" "domain_controller" {
  address_count      = 1
  address_family     = "ipv4"
  deployment_id      = organesson_deployment.forest.id
  logical_network_id = organesson_managed_network.forest.id
  name               = "domain-controller-static-ipv4"
  range_start        = "192.168.77.10"
  range_end          = "192.168.77.10"
}
