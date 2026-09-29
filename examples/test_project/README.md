# Team LAN provider example

`main.tf` is the current runnable local-provider example. It composes a deployment, logical groups, networks, VMs, network attachments, address reservations, an artifact, and guest-setup operations. It does not call Organesson or Proxmox yet.

`artifacts/first-time-setup.sh` is the future deployment artifact that will run directly from temporary read-only ISO media. `ansible/verify.yml` is the future post-provision verification playbook. Neither is executed by the current prototype.

Build and run the provider using [provider/README.md](../../provider/README.md).
