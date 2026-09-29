resource "proxmox_virtual_environment_vm" "pfsense" {
    name = "orgex-pfsense"
    description = "Organesson-managed router for the example routed lab"
    node_name = var.node_name
    tags = local.management_tags

    clone {
        full = true
        datastore_id = var.datastore_id
        vm_id = var.pfsense_template_vm_id
    }

    agent {
        enabled = true
    }

    network_device {
        bridge = "vmbr0"
    }

    network_device {
        bridge = local.lan_bridge
    }

    depends_on = [proxmox_sdn_applier.lab_lan]
}

# The attached ISO is declarative. Organesson's Kickstart boot adapter must add
# inst.ks=http://artifact-host/provisionings/<id>/fedora44-workstation.ks.
resource "proxmox_virtual_environment_vm" "fedora_workstation" {
    name = "orgex-fedora-workstation"
    description = "Fedora 44 Workstation installed by an Organesson Kickstart workflow"
    node_name = var.node_name
    tags = local.management_tags
    started = false

    agent {
        enabled = false
    }
    
    cdrom {
        file_id = var.fedora_workstation_iso_file_id
    }
    
    disk {
        datastore_id = var.datastore_id
        interface = "scsi0"
        size = 64
    }
    
    network_device {
        bridge = local.lan_bridge
    }

    depends_on = [proxmox_sdn_applier.lab_lan]
}

resource "proxmox_virtual_environment_vm" "fedora_server" {
    name = "orgex-fedora-server"
    description = "Fedora 44 Server clone; post-configured through the QEMU guest agent"
    node_name = var.node_name
    tags = local.management_tags

    clone {
        full = true
        datastore_id = var.datastore_id
        vm_id = var.fedora_server_template_vm_id
    }

    agent {
        enabled = true
    }

    disk {
        datastore_id = var.datastore_id
        interface = "scsi0"
        size = 80
    }

    network_device {
        bridge = local.lan_bridge
    }

    depends_on = [proxmox_sdn_applier.lab_lan]
}

resource "proxmox_virtual_environment_vm" "windows_11" {
    name = "orgex-windows-11"
    description = "Windows 11 sysprepped template clone; post-configured as SYSTEM"
    node_name = var.node_name
    tags = local.management_tags
    bios = "ovmf"

    clone {
        full = true
        datastore_id = var.datastore_id
        vm_id = var.windows_11_template_vm_id
    }

    agent {
        enabled = true
    }
  
    efi_disk {
        datastore_id = var.datastore_id
        type = "4m"
        pre_enrolled_keys = true
    }

    tpm_state {
        datastore_id = var.datastore_id
    }

    network_device {
        bridge = local.lan_bridge
    }

    depends_on = [proxmox_sdn_applier.lab_lan]
}
