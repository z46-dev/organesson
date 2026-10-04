# Alice's class-lab provider example

This is the Alice, Bob, Charlie, and Dave ownership/access scenario from [`docs/usecases/user_experience.md`](../../docs/usecases/user_experience.md). `@organesson` users are local test identities; this project defines deployment groups, logical ownership groups, and fixed permission grants in OpenTofu.

The topology has one `cyber.lab`-connected Fedora VM and one LAN-only Fedora VM per student. Each pair has a private static `/30` link. All LAN-only VMs also join one shared, isolated Proxmox SDN subnet and receive DHCP/DNS from a LAN-only Debian router clone. That router has no WAN NIC and cannot route or provide egress for the shared LAN. The internet NICs request addresses from the configured `cyber.lab` pool and retain that network's prefix, gateway, and DNS values; allocation does not carve out a new subnet.

## Proxmox modes

By default, VMs use the provider's simulated lifecycle. Two opt-in modes are available:

- `proxmox_lifecycle_smoke = true` clones Charlie's internet Fedora VM, attaches its `cyber.lab` NIC, claims one address, and applies its static guest configuration through QEMU Guest Agent. Other test resources stay simulated/declarative.
- `proxmox_test_deployment = true` provisions all four VMs, the per-student private SDN networks, the once-per-deployment shared DHCP subnet, all NIC attachments, and the guest IPv4 configurations. The current VM sizes request 8 vCPUs, 16 GiB RAM, and 256 GiB boot storage in total. It powers guests on for QGA setup.

The guest-network provider resource starts a stopped managed VM, waits for QEMU Guest Agent, writes a short-lived shell script to `/run`, and applies a MAC-bound NetworkManager connection. The script removes itself. Refresh checks the saved settings; destroy removes only that Organesson-managed connection before detaching NICs or deleting VMs.

Before applying, the administrator must validate a Proxmox resource policy with pool `organesson`, storage `laas`, sufficient optional capacity limits, the `cyber.lab` address pool, and `ogvxlan` selected as the VNet source. Proxmox `ogvxlan` is an administrator-owned VXLAN zone: Organesson creates marked VNets and subnet metadata in it but does not change the zone or configure Proxmox DHCP. The VNet subnet therefore needs an independent DHCP/DNS service. For this test that is a manual, LAN-only clone of source VM 106; its setup is in [the Debian router guide](../../docs/debian-router-source-vm.md). Do not attach a WAN NIC or enable routing on that clone. See the [Proxmox SDN documentation](https://github.com/proxmox/pve-docs/blob/master/pvesdn.adoc).

## Run the LAN-router test

From an empty OpenTofu state, create the shared VNet first so the router has a Proxmox network to attach to:

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" \
  tofu -chdir=examples/test_project apply \
  -target='organesson_network.shared_student_lan["shared"]' \
  -var='proxmox_test_deployment=true'
```

In Proxmox, full-clone source VM 106 to a new VMID in pool `organesson`, place its disk on `laas`, and attach its only NIC to the VNet shown by:

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" \
  tofu -chdir=examples/test_project state show \
  'organesson_network.shared_student_lan["shared"]'
```

Configure the clone with the LAN-only variant in the Debian router guide and start it. Then run the full `tofu apply` below. This ordering matters: the two Fedora LAN guests request DHCP during apply. The manual router clone is not in OpenTofu state; shut it down and delete it before destroying the test project so the managed VNet is no longer attached to an unmanaged VM.

Additional data disks and the Ansible verification playbook are still prototypes. `organesson_guest_setup` sends each declared local artifact to its Proxmox-backed Linux VM for one-time root execution through QEMU Guest Agent. That path has completed a focused live acceptance run in [`artifact_smoke`](../artifact_smoke/README.md); the broader Alice/Bob/Charlie/Dave topology separately covers VM clone, SDN network, address allocation, NIC attachment, guest IPv4 setup, permissions/power UI, refresh, and destroy.

## Run the single-VM lifecycle smoke

Follow [the Proxmox lifecycle checklist](../../docs/proxmox-vm-lifecycle.md) for source-catalog, policy, and API-token prerequisites. The smaller [`artifact_smoke`](../artifact_smoke/README.md) project is the moving acceptance target for new lifecycle features. From the repository root, build the local provider and set the documented provider environment variables, then run:

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project validate
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_lifecycle_smoke=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project apply -var='proxmox_lifecycle_smoke=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_lifecycle_smoke=true'
```

The second plan should show no changes. After applying, one Proxmox VM can be manually migrated to another node in the `ogvxlan` zone to test cross-node L2, DHCP, and the per-student private link; a subsequent plan should still show no changes. Before destroying the deployment, delete the manual router clone first, then destroy the OpenTofu resources with `-var='proxmox_test_deployment=true'`. Keep provider tokens and OpenTofu state private.
