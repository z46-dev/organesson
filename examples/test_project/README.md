# Team LAN provider example

`main.tf` is the current runnable local-provider example. It composes a deployment, logical groups, Organesson-resolved test users, deployment-local user groups with set-based membership, fixed permission grants, networks, VMs, independently attachable data disks, network attachments, guest network configuration, address reservations, an artifact package, and guest-setup operations. It does not call Organesson or Proxmox yet.

`artifacts/first-time-setup/` is source material for an immutable package, not a live guest filesystem. The future provider will package it, hash it, and deliver it through temporary read-only ISO media. `ansible/verify.yml` is the future post-provision verification playbook. Neither is executed by the current prototype.

Build and run the provider using [provider/README.md](../../provider/README.md).
