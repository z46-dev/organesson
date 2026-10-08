resource "organesson_address_pool_request" "cyber_ipv4" {
  address_count       = 1
  address_family      = "ipv4"
  deployment_id       = organesson_deployment.workstation.id
  environment_network = "cyber"
  name                = "cyber-ipv4"
}

resource "organesson_address_pool_request" "cyber_ipv6" {
  address_count       = 1
  address_family      = "ipv6"
  deployment_id       = organesson_deployment.workstation.id
  environment_network = "cyber"
  name                = "cyber-ipv6"
}
