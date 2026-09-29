# Team LAN provider example

`main.tf` is the current runnable local-provider example. It parses the requested deployment and logs the resources and offline setup workflow it would create. It does not call Organesson or Proxmox yet.

`artifacts/first-time-setup.sh` is the future deployment artifact that will run directly from temporary read-only ISO media. `ansible/verify.yml` is the future post-provision verification playbook. Neither is executed by the current prototype.

Build and run the provider using [provider/README.md](../../provider/README.md).
