# Proxmox lifecycle acceptance

The broader classroom integration fixture is [`examples/test_project`](../examples/test_project/README.md). With `proxmox_test_deployment=true`, it creates the Alice/Bob/Charlie/Dave resources, four Fedora VMs, isolated SDN networks, NIC attachments, address allocations, and guest IPv4 configuration. The validated policy must allow the selected pool and storage; the lab run uses pool `organesson` and storage `laas`.

The current focused lifecycle acceptance target and moving goalpost is [`examples/artifact_smoke`](../examples/artifact_smoke/README.md). Its live run verified VM clone, root artifact execution, success and failure cleanup, clean refresh, destroy, and preservation of the source VM. The example records the exact acceptance result and remains the fixture for extending artifact functionality.

## Safe live-test checklist

1. Use the backend's Proxmox connection configuration and confirm the Fedora Server source alias is ready. Do not use the source VM itself as a deployment resource.
2. In Administration → Proxmox resources, validate the policy for pool `organesson`, storage `laas`, the configured `cyber.lab` allocation, and isolated SDN networking. Ensure the cluster's DHCP integration is ready before running the shared DHCP network.
3. Build the local provider, copy the example's `tofu.rc.example` to an ignored `tofu.rc`, and set the repository path. Keep credentials and state private.
4. Run `tofu validate`, then `plan` and `apply` with the selected Proxmox mode. Refresh with another `plan`; it should show no changes.
5. Sign in as the expected fake users and check visibility and power access. An unauthorized cross-student action must be denied without changing the VM.
6. Run `tofu destroy` with the same mode flag. Confirm deployment VMs and networks are gone and the source VM/snapshots remain.

For source preparation, register the guest OS family in Administration → Source VMs and run **Prepare source**. The workflow invokes the bundled Linux, Windows, or FreeBSD prep script through QEMU Guest Agent, removes only accounts explicitly provided by the administrator, shuts down the guest, applies its valid tag, and only then restores readiness. QGA must already be installed and reachable as the initial bootstrap channel.
