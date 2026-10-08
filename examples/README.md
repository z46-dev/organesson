# OpenTofu examples

- [`it666_project`](it666_project/README.md) is the blank future project scaffold.
- [`test_project`](test_project/README.md) defines the full Alice/Bob/Charlie/Dave scenario.
- [`l2_smoke`](l2_smoke/README.md) is the minimal live fixture for validating a managed VNet over the configured Proxmox VXLAN source.
- [`cyber_workstation`](cyber_workstation/README.md) reserves IPv4 and IPv6 addresses on the `cyber` environment network for one Fedora workstation and grants view access to two `@cyber` users. It is an unapplied configuration example.

Use `l2_smoke` as the live-verified baseline for networking changes before expanding them into `test_project`. Keep local provider overrides, credentials, and state files untracked.
