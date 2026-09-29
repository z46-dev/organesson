variable "proxmox_endpoint" {
    type = string
}

variable "proxmox_api_token" {
    type = string
    sensitive = true
}

variable "proxmox_insecure_tls" {
    type = bool
    default = false
}

variable "node_name" {
    type = string
}

variable "datastore_id" {
    type = string
}

variable "sdn_zone_id" {
    type = string
}

variable "lan_vnet_id" {
    type = string
    default = "orgexlan"
}

variable "pfsense_template_vm_id" {
    type = number
}

variable "fedora_server_template_vm_id" {
    type = number
}

variable "windows_11_template_vm_id" {
    type = number
}

variable "fedora_workstation_iso_file_id" {
    type = string
}
