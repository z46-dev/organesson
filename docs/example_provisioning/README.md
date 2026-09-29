# Example provisioning: routed lab

This is a reference IaC definition for a small, non-exclusive PVE environment. It creates an Organesson-managed LAN, places a pfSense firewall between that LAN and the existing `vmbr0` WAN, and declares Fedora and Windows workloads behind it.

It demonstrates the intended contract between Organesson and a provisioning definition:

- PVE is an environment shared with other administrators; only resources tagged `organesson` and `org-example-routed-lab` belong to this definition.
- The LAN is an SDN VNet in a pre-existing Organesson-controlled SDN zone. `vmbr0` is referenced, never created or modified.
- Every template must start `qemu-guest-agent` and permit root/SYSTEM execution through it. A template without this capability is rejected during template registration.
- The HTTP artifact service is **HTTP-only by design**, so it must be reachable only on the lab network. Every artifact is SHA-256 pinned and fetched with a single-use, short-lived bearer token. The token must never be committed, placed in VM tags, or recorded in OpenTofu state.
- OpenTofu declares PVE resources. Guest configuration runs through Ansible or the guest-agent execution endpoint after creation; it does not use OpenTofu provisioners.

## Topology

```text
existing WAN: vmbr0 ── pfSense WAN (DHCP)
                          │
                  org-example-lan (SDN VNet)
                    ├── pfSense LAN: 10.42.0.1/24
                    ├── Fedora 44 Workstation: Kickstart ISO install
                    ├── Fedora 44 Server: template clone + post-config
                    └── Windows 11: sysprepped template clone + post-config
```

## Prerequisites

1. PVE SDN is configured, with an existing zone dedicated to Organesson (`tofu.sdn_zone_id`). Creating and applying an SDN VNet changes cluster networking; obtain the PVE administrator's approval first.
2. An Organesson-owned pfSense template, Fedora 44 Server template, and Windows 11 template exist. Each template has the guest agent enabled and tested.
3. The named Fedora 44 Workstation ISO already exists in PVE storage. Its file ID is passed as `fedora_workstation_iso_file_id`.
4. An artifact server is reachable on the new LAN. It serves the files under `artifacts/` at a per-provisioning HTTP URL, and validates a bearer token. The token is delivered at runtime through Organesson's secret channel.
5. The PVE API token is exported as `PROXMOX_VE_API_TOKEN`; do not put it in `terraform.tfvars`.

## Use

```sh
cd tofu
cp terraform.tfvars.example terraform.tfvars
# Fill in PVE names, IDs, and storage. Keep terraform.tfvars untracked.
tofu init
tofu plan
tofu apply
```

After OpenTofu creates the machines, Organesson should issue the declared post-provision actions using `scripts/pve-guest-exec.sh`, or call Ansible with dynamically generated guest inventory. `ansible/site.yml` is deliberately idempotent and is not invoked by OpenTofu.

## Important implementation decisions still required

- **Fedora Kickstart:** PVE must expose a supported way for Organesson to supply the kernel argument `inst.ks=http://.../fedora44-workstation.ks` at first boot. This might be a provider/API capability, a small generated boot ISO, or a console-automation adapter. The current OpenTofu provider can attach the ISO but does not model keystrokes; do not treat a VM with an attached ISO as an unattended installation until this adapter exists.
- **Windows:** The Windows template must be generalized (Sysprep), use virtio drivers, contain the QEMU guest agent service, and have a SYSTEM-level execution adapter. WinRM is not an acceptable replacement for the required agent path.
- **pfSense:** Confirm the exact guest-agent package/service on the selected pfSense release during template validation. Do not enable the PVE agent flag until the service starts successfully.
- **Token delivery:** Organesson should mint a token scoped to provisioning ID, artifact paths, target identity or network, and an expiry. It should revoke the token after bootstrap.

Provider notes: the `bpg/proxmox` provider supports VM clones, an enabled QEMU agent, ISO CD-ROMs, and SDN VNets. Its agent support must only be enabled when the service is actually running. See the [VM resource documentation](https://bpg.sh/docs/resources/virtual_environment_vm/) and [SDN VNet documentation](https://bpg.sh/docs/resources/sdn_vnet/). OpenTofu itself advises that provisioners are a last resort, hence the separate configuration phase. [OpenTofu provisioner guidance](https://opentofu.org/docs/language/resources/provisioners/syntax/)
