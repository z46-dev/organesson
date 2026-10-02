# Proxmox VM lifecycle milestone

The first live-provisioning slice is deliberately one QEMU VM, not the full Alice/Bob/Charlie/Dave classroom topology. It validates the control plane and ownership boundary before networking, disks, and guest setup are brought into the live path. The runnable acceptance project is [`examples/proxmox_vm_lifecycle`](../examples/proxmox_vm_lifecycle/README.md).

## Acceptance evidence

| Requirement | Evidence |
| --- | --- |
| Ready source, approved pool/storage, and global capacity are enforced before clone | Catalog readiness gate, current inventory revalidation, saved policy hash, quota reservation, and domain/API tests |
| PVE clone/configure tasks finish successfully before Organesson stores placement | Fake PVE HTTP test covers clone, task status polling, inherited NIC and installer-ISO removal, disk-first boot order, sizing, and disk growth |
| Provider create/read/delete keep one VM mapping | Authenticated provider-to-application integration test asserts one clone, stable VMID/node on refresh, and one managed-VM deletion |
| Stale IDs cannot control another VM | Driver requires the stored Organesson description marker before read, power, or delete; fake PVE test proves wrong markers cause no mutation |
| Power access follows resource grants and the UI shows live state | Authenticated application test exercises Charlie's view/power grant, live refresh, start/stop, and Dave's denied request; UI only renders controls when `can_power_control` is true |
| A human can test against a real cluster | The example README records the admin setup, `tofu plan/apply/plan`, UI power check, unauthorized-user check, and `tofu destroy` procedure |

## Current boundary

Supported: one managed QEMU VM cloned from a ready ordinary VM source; pool/storage and CPU/memory/boot-disk requests; policy validation and quota reservation; task waiting, placement persistence, retry recovery, live refresh, start/graceful-stop/restart API operations, and safe destroy. The UI's Stop action asks Proxmox to shut down the guest; it does not force-stop it. Inherited NICs and ISO-backed CD-ROMs are detached, and the clone is set to boot from its primary disk so a source's installer media or network cannot unexpectedly affect it.

The follow-on network slice now has API-backed address-pool reservations and creates policy-authorized isolated PVE SDN Simple-zone networks. It does not yet attach VM NICs, apply guest-side addresses, deliver artifacts, create extra disks, manage snapshots, or provision containers. Simple zones have no physical uplink and are node-local; DHCP additionally needs PVE's dnsmasq integration configured on each node. The broader `examples/test_project` still keeps networking dependent on this follow-on work.

## Safety notes

- Organesson reserves its ownership and capacity record before making an external clone request. A retry uses a stable operation key in the Proxmox description to recover a clone if the backend lost the original response.
- Only resources with a persisted Organesson external VMID/node mapping are candidates for power or destroy. The Proxmox description marker must still match that resource's saved operation key.
- VM deletion is requested from Proxmox and awaited before the Organesson record is removed. If Proxmox does not confirm deletion, Organesson retains its record for retry/reconciliation.
- The Proxmox API credential stays in backend configuration. OpenTofu uses a separate, revocable Organesson API token.
- A VM clone is not considered a full guest-provisioning workflow. In particular, this slice removes inherited NICs and installer media and does not install or configure guest software.
