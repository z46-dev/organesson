terraform {
    required_version = ">= 1.8.0"

    required_providers {
        proxmox = {
            source = "bpg/proxmox"
            version = ">= 0.90.0, < 1.0.0"
        }
    }
}

provider "proxmox" {
    endpoint = var.proxmox_endpoint
    api_token = var.proxmox_api_token
    insecure = var.proxmox_insecure_tls
}

locals {
    management_tags = ["organesson", "org-example-routed-lab"]
    lan_bridge = proxmox_sdn_vnet.lab_lan.id
}

resource "proxmox_sdn_vnet" "lab_lan" {
    id = var.lan_vnet_id
    alias = "Organesson example routed lab"
    isolate_ports = false
    zone = var.sdn_zone_id
}

# PVE requires an explicit SDN apply after changing VNet definitions.
resource "proxmox_sdn_applier" "lab_lan" {
    depends_on = [proxmox_sdn_vnet.lab_lan]
}
