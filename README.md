# organesson

A provisioning and management system for Proxmox VE

## What is it?

Oftentimes I find myself needing to provision and manage a lot of virtual machines, containers, and networks using Proxmox VE.

Here are two scenarios:

1. In the cybersecurity club, I want to set up a mini competition where there are multiple teams and each team gets a few linux containers on a flat network that they need to defend while attacking other teams.
2. For a course, I want to create a network which will have a pfSense for each student in the course. Each pfSense would have two networks and a few VMs across those networks, and also an Aruba virtualized switch.

Proxmox is by default very limiting. It's hard to manage users and assign resources to a specific user without cluttering the interface. There are also no built-in tools for automating the creation and management of complex network topologies using things like OpenTofu/Terraform and Ansible.

This project aims to change that.

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
