# Creating an Organesson source VM

Organesson “templates” are ordinary, editable Proxmox VMs used as clone sources. Do **not** convert them to Proxmox templates: an ordinary VM stays writable for maintenance, and Proxmox makes a full clone from it. Full clones copy the guest disks and use more storage than linked clones, but remain independent of the source. Organesson can map a stable name such as `og-template-fedora-server-latest` to its Proxmox VM ID. See the [Proxmox VM clone documentation](https://pve.proxmox.com/pve-docs/chapter-qm.html#qm_copy_and_clone).

## Minimal baseline

1. Create a normal VM from trusted installation media or an approved OS image. Choose the firmware and virtual hardware required by that OS. Connect it only to a controlled maintenance network while installing and updating it.
2. Install only the OS role needed. Apply updates, reboot, and confirm the machine is healthy.
3. Download and run the matching script from [`frontend/public/scripts/template-prep`](../frontend/public/scripts/template-prep/). These files are served by the frontend under `/scripts/template-prep/`. The Linux script updates supported systems, installs and checks QEMU Guest Agent and NetworkManager, configures its SELinux execution transition when that policy is active, then clears Linux clone identity. Before disabling NetworkManager's automatic fallback profiles, it tries to activate an Ethernet NIC and confirms it has a global address; if no usable NIC can be brought up, preparation stops without changing that fallback setting. Organesson configures each deployment NIC through NetworkManager, matching the guest interface to the Proxmox NIC MAC; the template setting prevents a new interface from being configured before Organesson applies its declared settings. On SELinux systems, full-privilege guest commands must use `/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec`; the script does not create or delete guest accounts. This wrapper permits arbitrary root commands through QEMU Guest Agent, so restrict guest-agent access to trusted management-plane users. Debian and Ubuntu keep their normal AppArmor configuration: the script leaves AppArmor unchanged and stops if QEMU Guest Agent is unexpectedly confined. For Windows, download the matching entry script **and** `og-prep-windows-common.ps1` into the same directory, and attach the VirtIO ISO. The scripts install its signed guest-agent MSI, apply Windows updates, and use Sysprep to generalize and shut down. If the agent install or a Windows update requires a reboot, the source remains unsealed: reboot and rerun the script. Enable the guest-agent option on the Proxmox VM as well. Before registering the source, test a harmless identity check with `qm guest exec <VMID> -- id -u` (Linux must return `0`) or `qm guest exec <VMID> -- whoami` (Windows must report `NT AUTHORITY\\SYSTEM`). If an OS cannot meet that requirement, document and validate an alternative privileged execution path before using it. Guest-agent execution is powerful: only register source VMs controlled by the Organesson administrators.
4. Remove environment-specific configuration and identity: static IPs, deployment network assumptions, personal accounts, passwords, SSH keys, machine identity, and host-specific settings. Do not bake deployment credentials or secrets into a source VM. If a maintenance account is unavoidable, use a unique, protected credential and disable or rotate it before provisioning is enabled.
5. Shut down the source VM. Make a disposable full clone, boot it on an isolated test network, and confirm it gets a unique identity and that guest-agent execution still works. Discard the test clone; leave the source VM powered off.
6. Register the stable Organesson source name and its Proxmox VM ID only after these checks pass.

## Catalog record

Each source VM is one catalog record with one or more unique aliases. The record holds its source platform and identifier, display name, broad guest family, OS name/version and architecture detected through QEMU Guest Agent, privileged execution method, and readiness state. Aliases are stable selectors such as `og-template-fedora-workstation-latest` and `og-template-fedora-server-latest`; a `latest` alias can be moved to a newly validated source without changing deployment configuration. Do not store passwords or other credentials in this catalog metadata.

In Administration → Source VMs, register the ordinary VM using only its VMID and broad guest type (Linux, Windows, or BSD/FreeBSD), then add aliases. During registration, Organesson powers it on if needed, checks QEMU Guest Agent and privileged execution, detects the OS name, version, and architecture, and shuts it down. Then use **Prepare source** or **Prepare / update** to update and configure the guest, optionally remove only the account names explicitly entered, verify root/SYSTEM execution, seal Windows with Sysprep, shut the VM down, and apply the `organesson-template-valid` Proxmox tag. Readiness is granted only after preparation succeeds. Preparation failure leaves the catalog entry unready and attempts graceful shutdown. **Check source** remains a read-only diagnostic. There is no manual readiness override. See the [VM lifecycle acceptance guide](proxmox-vm-lifecycle.md) for testing.

## Common OS cases

| Source | Keep in the baseline | Additional check |
| --- | --- | --- |
| Debian, Ubuntu, Linux Mint, Pop!_OS | Uses `apt` / `apt-get` | Confirm the guest-agent service is not confined by a custom AppArmor profile. |
| Fedora, RHEL, Rocky, AlmaLinux, CentOS, Oracle Linux, Amazon Linux | Uses `dnf` or falls back to `yum` | Ensure configured repositories provide updates and `qemu-guest-agent`. |
| openSUSE, SLES, SLED | Uses `zypper` | Confirm the installed release provides QEMU Guest Agent and NetworkManager packages. |
| Alpine Linux | Uses `apk` and OpenRC when available | Verify guest-agent and NetworkManager service names on the selected release. |
| Arch Linux, Manjaro, EndeavourOS | Uses `pacman` | Confirm the rolling system is fully updated and guest-agent services are enabled. |
| FreeBSD | Uses `pkg` and rc.conf/service | Install and enable QEMU Guest Agent before importing so it can execute the prep workflow. The adapter currently targets FreeBSD, not OpenBSD/NetBSD. |
| Windows 11 workstation | The catalog workflow selects `og-prep-windows11.ps1` and its common helper | Use the appropriate UEFI/TPM configuration. Install and enable QEMU Guest Agent first; the workflow applies Windows Update, removes only named non-built-in accounts, runs Sysprep, and shuts down. |
| Windows Server 2025 | The catalog workflow selects `og-prep-windows-server2025.ps1` and its common helper | Install and enable QEMU Guest Agent first. The workflow applies Windows Update, removes only named non-built-in accounts, runs Sysprep, and shuts down. |

For a Debian 13 router source VM with NetworkManager, `nftables`, DHCP/DNS, clone-time address handling, and a clone acceptance checklist, see [Debian 13 router source VM](debian-router-source-vm.md).

## Updating a source

Use **Prepare / update** for a catalog source. This automatically removes it from provisioning while the run is in progress, executes the prep workflow through QGA, and restores readiness only after successful validation and shutdown. Test a disposable clone before relying on a newly maintained source. Update a `latest` alias only after validation; keep version-pinned source mappings separate.

The catalog does not yet schedule maintenance or automatically test a disposable clone.
