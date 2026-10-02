# Test project acceptance fixture

The runnable configuration is in [`examples/test_project`](../../examples/test_project). It is the concrete Alice, Bob, Charlie, and Dave scenario from [`user_experience.md`](user_experience.md), used to test the provider, Organesson API, provisioning worker, and permission-aware UI.

The deployment is owned by the platform-administrator ownership node. It defines these deployment-local user groups:

- `charlie`, containing `charlie@organesson`, and owning Charlie's logical lab.
- `dave`, containing `dave@organesson`, and owning Dave's logical lab.
- `students`, containing Charlie and Dave.
- `teaching-staff`, containing `alice@organesson` and `bob@organesson`.

Each student lab contains:

1. An unmanaged Layer-2 `private-link` between its two Fedora VMs.
2. One dual-homed Fedora Server VM with a DHCP-attached `cyber.lab` NIC and private-link address `192.168.50.1/30`.
3. One Fedora Server VM with private-link address `192.168.50.2/30`, plus a DHCP attachment to the deployment-wide shared network.
4. A 500 GiB data disk attached to the dual-homed Fedora VM.
5. One artifact-driven first-time setup operation for each VM.

The deployment also creates one managed Proxmox SDN-backed network, `shared-student-lan`, with subnet `192.168.100.0/24`, gateway `192.168.100.1`, DHCP enabled, and an `isolated` egress policy. It has no uplink. All `lan_fedora` VMs attach to this one network and receive their guest-side configuration through DHCP.

The deployment requests two IPv4 addresses from the `cyber.lab` environment network through one address-pool request. Each dual-homed Fedora VM consumes one address from that specific pool. A provider and the eventual Organesson API must reject an attachment that requests addresses from a pool belonging to a different deployment or environment network, or when total interface requests exceed the pool's approved capacity.

Permissions are fixed Organesson capabilities:

- Charlie and Dave: `resource.view`, `vm.power_control`, and `vm.console_control` on their own logical lab and descendants.
- Teaching staff: view, power, console, and snapshot control on the deployment and descendants.
- Alice: deployment user, group, permission, and configuration management on the deployment itself.

The package never creates test users. `@organesson` references a test identity source seeded by Organesson during application development.

## Current lifecycle acceptance boundary

The full Alice/Bob/Charlie/Dave topology has been applied and destroyed against the lab using `proxmox_test_deployment = true` in [`examples/test_project`](../../examples/test_project/README.md). The exercised path includes four Proxmox VMs, isolated SDN networks, address allocation, NIC attachment, guest IPv4 setup, permission-aware access, refresh, and destroy. The lab policy uses pool `organesson` and storage `laas`.

Extra virtual disks remain declarations only. Artifact packaging and root execution have API/fake-Proxmox coverage and a completed live acceptance run using [`examples/artifact_smoke`](../../examples/artifact_smoke/README.md); the Alice/Bob/Charlie/Dave topology itself does not yet include that live artifact verification. See the [Proxmox lifecycle checklist](../proxmox-vm-lifecycle.md) before running against a live cluster.
