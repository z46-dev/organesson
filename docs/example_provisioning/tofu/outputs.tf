output "managed_resource_names" {
    value = {
        pfsense = proxmox_virtual_environment_vm.pfsense.name
        fedora_server = proxmox_virtual_environment_vm.fedora_server.name
        fedora_workstation = proxmox_virtual_environment_vm.fedora_workstation.name
        windows_11 = proxmox_virtual_environment_vm.windows_11.name
    }
}

output "lan_vnet" {
    value = proxmox_sdn_vnet.lab_lan.id
}
