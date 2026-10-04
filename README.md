# organesson

A provisioning and management system for Proxmox VE

## What is it?

Oftentimes I find myself needing to provision and manage a lot of virtual machines, containers, and networks using Proxmox VE.

Here are two scenarios:

1. In the cybersecurity club, I want to set up a mini competition where there are multiple teams and each team gets a few linux containers on a flat network that they need to defend while attacking other teams.
2. For a course, I want to create a network which will have a pfSense for each student in the course. Each pfSense would have two networks and a few VMs across those networks, and also an Aruba virtualized switch.

Proxmox is by default very limiting. It's hard to manage users and assign resources to a specific user without cluttering the interface. There are also no built-in tools for automating the creation and management of complex network topologies using things like OpenTofu/Terraform and Ansible.

This project aims to change that.

## Proxmox Cluster Setup

Organesson needs a cluster-wide Layer 2 network when VMs on different Proxmox nodes must share the same LAN. A Proxmox SDN Simple zone is isolated to one host; a VXLAN zone carries Ethernet frames between nodes over an IP underlay. Create one reusable VXLAN zone for the cluster, then give each independent network its own VNet and unique VXLAN ID (VNI). You do not need one VXLAN zone per deployment or per VNet.

On current Proxmox VE nodes, SDN core is included. Before creating the zone, make sure every participating node has a stable IP address reachable by every other node over the existing underlay (for this lab, that is the node-to-node network on `vmbr0`) and that UDP port `4789` is allowed between those addresses in both directions. VXLAN does not encrypt traffic; keep the underlay trusted or protect it with a secure transport. VXLAN also adds 50 bytes of encapsulation overhead, so use an MTU of `1450` over a `1500`-byte underlay, or adjust both sides consistently for a different underlay MTU. Do not change `vmbr0` just to create the overlay.

Quick setup in the Proxmox web UI:

1. Open **Datacenter → SDN → Zones → Create → VXLAN**.
2. Use a short zone ID such as `ogvxlan`, select every node that may host Organesson VMs, and list the underlay IP address of every participating node as a peer. Set MTU to `1450` for a standard `1500`-byte underlay.
3. Save the zone, then go to **Datacenter → SDN** and apply the pending configuration cluster-wide.
4. Create a VNet in that zone for a quick connectivity check. Give it a unique VNet ID and an unused VNI, apply the pending SDN configuration again, then attach test VMs on different nodes and verify they can communicate on the same guest subnet.

The VXLAN zone provides Layer 2 transport only. It does not provide a gateway, DHCP, DNS, or internet egress; attach a router/service VM or use another managed network service when those are needed. In Organesson, select the zone as **VNet source** under **Administration → Quotas & placement** and validate/save. Each created Organesson network then receives its own managed VNet and an available VNI in that zone; the shared source zone remains untouched.

See the [Proxmox VE SDN documentation](https://github.com/proxmox/pve-docs/blob/master/pvesdn.adoc) for zone/VNet options and version-specific details.

## Supported Resources Stuff

- Virtual Machines
    - Via cloning pre-built templates or
    - Via manual creation using cloudinit/kickstart/etc
- Containers
    - Via cloning pre-built templates
- Virtual Networks
    - Only for Proxmox resources
- Potential Bare Metal Servers
    - Via manual creation using PXE boot and
    - Cloudinit/kickstart/etc

Bare metal is mentioned but not currently planned for support. It would be nice to support it in the future to support more complex deployment scenarios when more compute is needed.

---

## Tech Stack

- Backend in Go
    - github.com/z46-dev/golog for logging
    - github.com/z46-dev/gasket for task management
    - github.com/z46-dev/gosqlite for database operations using struct tags for mapping to database tables
    - github.com/z46-dev/goconf for configuration management with struct tags
    - github.com/gofiber/fiber/v3 for the web framework
    - unknown for auth and acl management
    - unknown for LDAP (specifically FreeIPA) integration
    - unknown for integration with Proxmox VE (MUST support virtual console so we can give users console to their VMs, as well as qemu guest agent execution)
- Frontend in React + Shadcn + TailwindCSS using TypeScript with Bun & Vite
