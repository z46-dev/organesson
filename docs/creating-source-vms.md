# Creating an Organesson source VM

Organesson “templates” are ordinary, editable Proxmox VMs used as clone sources. Do **not** convert them to Proxmox templates: an ordinary VM stays writable for maintenance, and Proxmox makes a full clone from it. Full clones copy the guest disks and use more storage than linked clones, but remain independent of the source. Organesson can map a stable name such as `og-template-fedora-server-latest` to its Proxmox VM ID. See the [Proxmox VM clone documentation](https://pve.proxmox.com/pve-docs/chapter-qm.html#qm_copy_and_clone).

## Minimal baseline

1. Create a normal VM from trusted installation media or an approved OS image. Choose the firmware and virtual hardware required by that OS. Connect it only to a controlled maintenance network while installing and updating it.
2. Install only the OS role needed. Apply updates, reboot, and confirm the machine is healthy.
3. Download and run the matching script from [`frontend/public/scripts/template-prep`](../frontend/public/scripts/template-prep/). These files are served by the frontend under `/scripts/template-prep/`. The Linux script updates supported systems, installs and checks QEMU Guest Agent, configures its SELinux execution transition when that policy is active, then clears Linux clone identity. It preserves the network manager already used by the guest: NetworkManager systems get the no-auto-default setting, while wicked and systemd-networkd systems keep their existing service and interface configuration. Preparation confirms an Ethernet NIC has a global address before it continues. Organesson's current deployment-time Linux NIC configurator uses NetworkManager; wicked- and networkd-based sources can be prepared safely, but deployment NIC automation for those managers remains unsupported. On SELinux systems, full-privilege guest commands must use `/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec`; the script does not create or delete guest accounts. This wrapper permits arbitrary root commands through QEMU Guest Agent, so restrict guest-agent access to trusted management-plane users. Debian and Ubuntu keep their normal AppArmor configuration: the script leaves AppArmor unchanged and stops if QEMU Guest Agent is unexpectedly confined. For Windows, attach a trusted VirtIO ISO and run the single `og-prep-windows.ps1` script from an elevated PowerShell window. It detects Windows 11 or Server 2025, installs the matching VirtIO serial driver, and locates the QEMU Guest Agent MSI on the VirtIO CD drive. Some VirtIO releases ship that MSI without an Authenticode signature; the script permits an unsigned MSI only from the mounted optical drive, but rejects a present invalid signature. For the initial bootstrap, download it from `https://<organesson-host>/scripts/template-prep/windows/og-prep-windows.ps1` and run it with `-BootstrapOnly`; then enable the guest-agent option in Proxmox (restart the VM if needed) and test `qm guest exec <VMID> -- whoami` (expect `NT AUTHORITY\\SYSTEM`). Windows updates are intentionally not run by the script; patch the source manually when desired and reboot it. Keep a local administrator signed in at the VM console when running Organesson's **Prepare source** action: QGA runs commands as SYSTEM, so Organesson launches Sysprep in that administrator's interactive session. It then makes a disposable full clone on the same pool/storage, preserves the guest-visible NICs but sets each link down before first boot, and accepts Windows reaching OOBE or completing OOBE and reaching the desktop. The preparation dialog shows a live, copyable progress log. Setup logs are included in a failed validation error when QGA remains reachable. The temporary clone is deleted after the check. Linux should pass `qm guest exec <VMID> -- id -u` with `0`. If an OS cannot meet the privileged execution requirement, document and validate an alternative before using it. Guest-agent execution is powerful: only register source VMs controlled by Organesson administrators.

Windows **Prepare source** checks the OS volume's BitLocker conversion state. If encrypted, it starts decryption and waits for the volume to be fully decrypted before Sysprep; this removes the OS volume's encryption and key protectors on the source. Data volumes are not changed. Re-enable BitLocker later on deployed VMs if required.

Some RHEL-family QEMU Guest Agent packages ship an RPC allowlist that omits `guest-exec`. Since Organesson cannot launch a command to change a command that QGA itself blocks, run the Linux prep script locally as root from the guest console for the first preparation. It updates recognized `/etc/sysconfig/qemu-ga` filters to include the guest-exec and file-transfer commands Organesson needs while preserving the other RPC policy. Restart the agent with `systemctl restart qemu-guest-agent` after the script finishes, then run preparation from Organesson. QGA RPC filters and their allow/block semantics are documented by [QEMU](https://www.qemu.org/docs/master/interop/qemu-ga.html); allowing `guest-exec` enables privileged execution through the management plane.
4. Remove environment-specific configuration and identity: static IPs, deployment network assumptions, personal accounts, passwords, SSH keys, machine identity, and host-specific settings. Do not bake deployment credentials or secrets into a source VM. If a maintenance account is unavoidable, use a unique, protected credential and disable or rotate it before provisioning is enabled.
5. Shut down the source VM. Make a disposable full clone, boot it on an isolated test network, and confirm it gets a unique identity and that guest-agent execution still works. Discard the test clone; leave the source VM powered off.
6. Register the stable Organesson source name and its Proxmox VM ID only after these checks pass.

## Catalog record

Each source VM is one catalog record with one or more unique aliases. The record holds its source platform and identifier, display name, broad guest family, OS name/version and architecture detected through QEMU Guest Agent, privileged execution method, and readiness state. Aliases are stable selectors such as `og-template-fedora-workstation-latest` and `og-template-fedora-server-latest`; a `latest` alias can be moved to a newly validated source without changing deployment configuration. Do not store passwords or other credentials in this catalog metadata.

In Administration → Source VMs, register the ordinary VM using only its VMID and broad guest type (Linux, Windows, or BSD/FreeBSD), then add aliases. During registration, Organesson powers it on if needed, checks QEMU Guest Agent and privileged execution, detects the OS name, version, and architecture, and shuts it down. Then use **Prepare source** or **Prepare / update** to update and configure the guest, optionally remove only the account names explicitly entered, verify root/SYSTEM execution, seal Windows with Sysprep, shut the VM down, and apply the `organesson-template-valid` Proxmox tag. Windows readiness additionally requires successful specialize/OOBE validation on a NIC-disconnected disposable clone; it passes whether validation stops at OOBE or an administrator completes OOBE and reaches the desktop. Preparation progress remains visible and copyable in the dialog. Preparation failure leaves the catalog entry unready and attempts graceful shutdown. **Check source** remains a read-only diagnostic. There is no manual readiness override. See the [VM lifecycle acceptance guide](proxmox-vm-lifecycle.md) for testing.

## Common OS cases

| Source | Keep in the baseline | Additional check |
| --- | --- | --- |
| Debian, Ubuntu, Linux Mint, Pop!_OS | Uses `apt` / `apt-get`; preserves active NetworkManager or systemd-networkd | Confirm the guest-agent service is not confined by a custom AppArmor profile. |
| Fedora, RHEL, Rocky, AlmaLinux, CentOS, Oracle Linux, Amazon Linux | Uses `dnf` or falls back to `yum` | Ensure configured repositories provide updates and `qemu-guest-agent`. |
| openSUSE, SLES, SLED | Uses `zypper`; preserves the guest's existing NetworkManager or wicked configuration | Confirm the installed release provides QEMU Guest Agent and its selected network-manager tooling. Runtime Organesson NIC configuration currently requires NetworkManager. |
| Alpine Linux | Uses `apk` and OpenRC when available | Verify guest-agent and NetworkManager service names on the selected release. |
| Arch Linux, Manjaro, EndeavourOS | Uses `pacman` | Confirm the rolling system is fully updated and guest-agent services are enabled. |
| FreeBSD | Uses `pkg` and rc.conf/service | Load and persist the `virtio_console` driver, verify `/dev/vtcon/org.qemu.guest_agent.0`, then start and test QEMU Guest Agent before importing. The adapter currently targets FreeBSD, not OpenBSD/NetBSD. |
| Windows 11 workstation | The unified `og-prep-windows.ps1` detects the edition | Use the appropriate UEFI/TPM configuration. Bootstrap QGA from the VirtIO ISO; patch manually when desired. Keep an administrator signed in while Organesson runs Sysprep and validates a disposable first boot. |
| Windows Server 2025 | The unified `og-prep-windows.ps1` detects the edition | Bootstrap QGA from the VirtIO ISO; patch manually when desired. Keep an administrator signed in while Organesson runs Sysprep and validates a disposable first boot. |

### FreeBSD / pfSense QEMU Guest Agent

FreeBSD needs the `virtio_console` driver for the Proxmox QEMU Guest Agent channel. Before first importing a pfSense source whose agent is not yet reachable after boot, open its guest console as root and run:

```sh
kldload virtio_console
sysrc -f /boot/loader.conf virtio_console_load="YES"
sysrc qemu_guest_agent_enable="YES"
service qemu-guest-agent start
test -e /dev/vtcon/org.qemu.guest_agent.0
```

Then verify from the Proxmox node with `qm agent <VMID> ping` and `qm guest exec <VMID> -- /usr/bin/id`. The FreeBSD prep script also loads the driver for the current boot, persists it for future boots, checks the agent channel, and enables the service. `loader.conf` uses the `<module>_load="YES"` setting to load kernel modules during boot ([FreeBSD loader.conf(5)](https://man.freebsd.org/cgi/man.cgi?manpath=FreeBSD+14.3-RELEASE&query=loader.conf&sektion=5)).

For a Debian 13 router source VM with NetworkManager, `nftables`, DHCP/DNS, clone-time address handling, and a clone acceptance checklist, see [Debian 13 router source VM](debian-router-source-vm.md).

## Updating a source

Use **Prepare / update** for a catalog source. This automatically removes it from provisioning while the run is in progress, executes the prep workflow through QGA, and restores readiness only after successful validation and shutdown. Test a disposable clone before relying on a newly maintained source. Update a `latest` alias only after validation; keep version-pinned source mappings separate.

The catalog does not yet schedule source maintenance.
