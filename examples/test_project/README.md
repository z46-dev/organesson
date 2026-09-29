# Alice's class-lab provider example

This is the runnable acceptance fixture for the Alice, Bob, Charlie, and Dave scenario in [`docs/usecases/user_experience.md`](../../docs/usecases/user_experience.md).

- Alice teaches the class and receives deployment-management permissions.
- Bob is the TA and, with Alice, belongs to `teaching-staff`; that group can view, power-control, console-control, and manage snapshots for the deployment.
- Charlie and Dave each receive an isolated logical lab. Each can view, power-control, and console-control only their own lab and its descendants.
- The deployment requests one `cyber.lab` IPv4 pool address per student. Each dual-homed Fedora VM consumes one address from that pool and receives it through DHCP.
- Every student has a private, unmanaged Layer-2 link between their two Fedora VMs, using static `/30` addresses.
- A single managed, Proxmox SDN-backed `shared-student-lan` is created for the deployment. Every `lan_fedora` connects to it and receives a DHCP address from its isolated `192.168.100.0/24` subnet; it has no uplink.

`@organesson` identifies users from Organesson's future built-in test identity source. This configuration creates no users in an external identity system. Deployment-local groups, their set-based membership, logical resource groups, and fixed permission grants are defined across the root `.tf` files.

Both Fedora templates must provide QEMU Guest Agent support. `artifacts/first-time-setup/` is source material for an immutable package, not a live guest filesystem. The future provider will package it, hash it, and deliver it through temporary read-only ISO media. `ansible/verify.yml` is the future post-provision verification playbook. Neither is executed by the current prototype.

The current provider parses and validates this configuration locally; it does not call Organesson or Proxmox yet. Build and run it using [provider/README.md](../../provider/README.md).
