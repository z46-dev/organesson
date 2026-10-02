# Alice's class-lab provider example

This is the Alice, Bob, Charlie, and Dave ownership/access scenario from [`docs/usecases/user_experience.md`](../../docs/usecases/user_experience.md). `@organesson` users are local test identities; this project defines deployment groups, logical ownership groups, and fixed permission grants in OpenTofu.

The topology has one internet-facing Fedora VM and one LAN-only Fedora VM per student. Each pair has a private static `/30` link. All LAN-only VMs also join one shared, isolated Proxmox SDN subnet and use DHCP there. The internet NICs request addresses from the configured `cyber.lab` pool and retain that network's prefix, gateway, and DNS values; allocation does not carve out a new subnet.

## Proxmox modes

By default, VMs use the provider's simulated lifecycle. Two opt-in modes are available:

- `proxmox_lifecycle_smoke = true` clones Charlie's internet Fedora VM, attaches its `cyber.lab` NIC, claims one address, and applies its static guest configuration through QEMU Guest Agent. Other test resources stay simulated/declarative.
- `proxmox_test_deployment = true` provisions all four VMs, the per-student private SDN networks, the once-per-deployment shared DHCP subnet, all NIC attachments, and the guest IPv4 configurations. The current VM sizes request 8 vCPUs, 16 GiB RAM, and 256 GiB boot storage in total. It powers guests on for QGA setup.

The guest-network provider resource starts a stopped managed VM, waits for QEMU Guest Agent, writes a short-lived shell script to `/run`, and applies a MAC-bound NetworkManager connection. The script removes itself. Refresh checks the saved settings; destroy removes only that Organesson-managed connection before detaching NICs or deleting VMs.

Before a full apply, the administrator must validate a Proxmox resource policy with the selected resource pool/storage, sufficient optional capacity limits, the `cyber.lab` address pool, and isolated SDN networking enabled. Proxmox Simple-zone DHCP uses its dnsmasq integration, which requires the `dnsmasq` package on every node that can host these guests; the shared VNet has no physical uplink or SNAT. See the [Proxmox SDN documentation](https://github.com/proxmox/pve-docs/blob/master/pvesdn.adoc). This example's Debian/Proxmox host prerequisite must be handled before enabling DHCP; Organesson does not install host packages.

Additional data disks and the Ansible verification playbook are still prototypes. `organesson_guest_setup` sends each declared local artifact to its Proxmox-backed Linux VM for one-time root execution through QEMU Guest Agent. That path has completed a focused live acceptance run in [`artifact_smoke`](../artifact_smoke/README.md); the broader Alice/Bob/Charlie/Dave topology separately covers VM clone, SDN network, address allocation, NIC attachment, guest IPv4 setup, permissions/power UI, refresh, and destroy.

## Run the single-VM lifecycle smoke

Follow [the Proxmox lifecycle checklist](../../docs/proxmox-vm-lifecycle.md) for source-catalog, policy, and API-token prerequisites. The smaller [`artifact_smoke`](../artifact_smoke/README.md) project is the moving acceptance target for new lifecycle features. From the repository root, build the local provider and set the documented provider environment variables, then run:

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project validate
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_lifecycle_smoke=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project apply -var='proxmox_lifecycle_smoke=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_lifecycle_smoke=true'
```

The second plan should show no changes. The full deployment uses the same sequence with `-var='proxmox_test_deployment=true'` and should be destroyed with the same variable. Keep provider tokens and OpenTofu state private.
