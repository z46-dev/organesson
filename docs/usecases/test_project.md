This project is a smaller project to be used to test during development.

For this deployment, we shall:

1. Request an IPv4 range containing at least 4 addresses.
2. For each of `[team 1, team 2, team 3]` we will:
    1. Create a VNet called "LAN" with no subnet
    2. Create a VM from the template `og-template-fedora-workstation-stable` called `primary`.
        - 4 CPU, 8 GB RAM, 128 GB Disk, WAN NIC, LAN NIC
        - WAN NIC will be assigned an IP from the requested range
        - LAN NIC will be assigned to the "LAN" VNet
        - User configured: `administrator:password123`
        - LAN nic configured with static IP `192.168.50.1/30`
    3. Create a VM from the template `og-template-fedora-workstation-stable` called `secondary`.
        - 4 CPU, 8 GB RAM, 128 GB Disk, LAN NIC
        - LAN NIC will be assigned to the "LAN" VNet
        - User configured: `administrator:password123`
        - LAN nic configured with static IP `192.168.50.2/30`