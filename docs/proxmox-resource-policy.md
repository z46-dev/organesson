# Proxmox resource policy

- Platform administrators set optional global CPU, memory, and storage limits; `0` means unlimited.
- At least one Proxmox resource pool and storage must be selected before the policy can be saved.
- Organesson network labels map to Proxmox Linux bridges or SDN VNets. Address pools declare the original prefix, allocatable range, gateway, and DNS servers. Deployment address requests are persisted, skip gateway/DNS and existing Organesson reservations, and never rewrite the source prefix or route settings.
- `allow_isolated_sdn_networks` separately permits deployment-owned Proxmox SDN Simple zones and VNets. These are isolated L2 networks, not uplinks; they are local to each PVE node, so guests must be placed on a common node unless the lab provides another supported fabric.
- DHCP on a Simple-zone VNet requires PVE's SDN DHCP integration and dnsmasq on every node where guests may run. Organesson does not install host packages or configure node services. Verify that prerequisite before using a network with `dhcp_enabled = true`; this cluster currently lacks the dnsmasq service.
- “Validate and save” reads current PVE pools, storage, bridges, and VNets, and saves only when every selected resource is visible in that inventory. It does not verify future write permissions or change Proxmox.
- PVE inventory cannot prove that an IP range on a bridge is reserved for Organesson or excluded from external DHCP. Admins must keep the declared pool out of all other address managers.
- Saving the policy does not reserve capacity. Address reservations occur when an OpenTofu `organesson_address_pool_request` is applied and are released when that resource is destroyed.
- The policy is stored in SQLite. Proxmox API credentials remain in the backend configuration.
