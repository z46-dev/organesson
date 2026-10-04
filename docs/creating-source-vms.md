# Creating an Organesson source VM

Organesson “templates” are ordinary, editable Proxmox VMs used as clone sources. Do **not** convert them to Proxmox templates: an ordinary VM stays writable for maintenance, and Proxmox makes a full clone from it. Full clones copy the guest disks and use more storage than linked clones, but remain independent of the source. Organesson can map a stable name such as `og-template-fedora-server-latest` to its Proxmox VM ID. See the [Proxmox VM clone documentation](https://pve.proxmox.com/pve-docs/chapter-qm.html#qm_copy_and_clone).

## Minimal baseline

1. Create a normal VM from trusted installation media or an approved OS image. Choose the firmware and virtual hardware required by that OS. Connect it only to a controlled maintenance network while installing and updating it.
2. Install only the OS role needed. Apply updates, reboot, and confirm the machine is healthy.
3. Download and run the matching script from [`frontend/public/scripts/template-prep`](../frontend/public/scripts/template-prep/). These files are served by the frontend under `/scripts/template-prep/`. The Linux script updates supported systems, installs and checks QEMU Guest Agent, configures its SELinux execution transition when that policy is active, then clears Linux clone identity. On SELinux systems, full-privilege guest commands must use `/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec`; the script does not create or delete guest accounts. This wrapper permits arbitrary root commands through QEMU Guest Agent, so restrict guest-agent access to trusted management-plane users. Debian and Ubuntu keep their normal AppArmor configuration: the script leaves AppArmor unchanged and stops if QEMU Guest Agent is unexpectedly confined. For Windows, download the matching entry script **and** `og-prep-windows-common.ps1` into the same directory, and attach the VirtIO ISO. The scripts install its signed guest-agent MSI, apply Windows updates, and use Sysprep to generalize and shut down. If the agent install or a Windows update requires a reboot, the source remains unsealed: reboot and rerun the script. Enable the guest-agent option on the Proxmox VM as well. Before registering the source, test a harmless identity check with `qm guest exec <VMID> -- id -u` (Linux must return `0`) or `qm guest exec <VMID> -- whoami` (Windows must report `NT AUTHORITY\\SYSTEM`). If an OS cannot meet that requirement, document and validate an alternative privileged execution path before using it. Guest-agent execution is powerful: only register source VMs controlled by the Organesson administrators.
4. Remove environment-specific configuration and identity: static IPs, deployment network assumptions, personal accounts, passwords, SSH keys, machine identity, and host-specific settings. Do not bake deployment credentials or secrets into a source VM. If a maintenance account is unavoidable, use a unique, protected credential and disable or rotate it before provisioning is enabled.
5. Shut down the source VM. Make a disposable full clone, boot it on an isolated test network, and confirm it gets a unique identity and that guest-agent execution still works. Discard the test clone; leave the source VM powered off.
6. Register the stable Organesson source name and its Proxmox VM ID only after these checks pass.

## Catalog record

Each source VM is one catalog record with one or more unique aliases. The record holds its source platform and identifier, display name, OS/version/edition, architecture, privileged execution method, and whether it has passed provisioning-readiness checks. Aliases are stable selectors such as `og-template-fedora-workstation-latest` and `og-template-fedora-server-latest`; a `latest` alias can be moved to a newly validated source without changing deployment configuration. Do not store passwords or other credentials in this catalog metadata. A source must be running during preflight: for Linux, Organesson checks the reported OS and verifies QEMU Guest Agent commands run as UID 0 outside Fedora's confined `virt_qemu_ga_t` SELinux domain. On SELinux systems, run `og-prep-linux.sh` first so the labeled execution wrapper is available. An administrator also confirms the temporary provisioning account was removed. Windows system-level execution remains an explicit administrator check. See the [VM lifecycle acceptance guide](proxmox-vm-lifecycle.md) for a live single-VM test.

## Common OS cases

| Source | Keep in the baseline | Additional check |
| --- | --- | --- |
| Fedora Server / Workstation 43–45 | Supported by `og-prep-linux.sh`; the script handles both editions | Fedora 45 is currently pre-release; verify it again after final media is published. Confirm the clone regenerates machine identity and SSH host keys. |
| Ubuntu Desktop / Server 24.04 and 26.04 LTS | Supported by `og-prep-linux.sh`; the script handles both editions | These are the latest two Ubuntu LTS releases. Confirm the desktop or server role remains as intended after cloning. |
| Debian Server / Desktop 12–13 | Supported by `og-prep-linux.sh`; the script handles both editions | Debian 13 is current stable; Debian 12 is in LTS. Confirm the guest-agent service is not confined by a custom AppArmor profile. |
| RHEL, Rocky Linux, AlmaLinux 8–10 | Supported by `og-prep-linux.sh`; server-oriented installs are the expected baseline | Use a currently supported minor release and ensure its configured repositories provide updates and `qemu-guest-agent`. |
| Windows 11 workstation | Run `og-prep-windows11.ps1` with `og-prep-windows-common.ps1` beside it | Use the appropriate UEFI/TPM configuration. The script uses Windows Update, installs the signed guest-agent MSI from the attached VirtIO ISO, and runs Sysprep. |
| Windows Server 2025 | Run `og-prep-windows-server2025.ps1` with `og-prep-windows-common.ps1` beside it | The script uses Windows Update, installs the signed guest-agent MSI from the attached VirtIO ISO, and runs Sysprep. |

For a Debian 13 router source VM with NetworkManager, `nftables`, DHCP/DNS, clone-time address handling, and a clone acceptance checklist, see [Debian 13 router source VM](debian-router-source-vm.md).

## Updating a source

Temporarily mark the source unavailable for new provisioning. Boot it on the maintenance network and run its prep script. For Linux, remove the downloaded script after success and shut the source down without rebooting; the script has already cleared its clone identity. For Windows, the successful script removes both downloaded prep files and invokes Sysprep to shut down. Test a disposable clone again before making the source available. Update a `latest` mapping only after validation; keep version-pinned source mappings separate.

This is an operator procedure for the current development stage. Organesson does not yet manage source-VM registration, availability, or update scheduling automatically.
