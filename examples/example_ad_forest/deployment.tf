resource "organesson_deployment" "forest" {
  name        = "example-ad-forest"
  owner       = "system:platform-administrators"
}

resource "organesson_logical_group" "forest" {
  deployment_id = organesson_deployment.forest.id
  name          = "ad-forest"
}
