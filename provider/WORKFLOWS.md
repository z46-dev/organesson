# Guest workflows

`organesson_guest_workflow` runs a sequence of scripts on Organesson-managed VMs during `tofu apply`. Use it when setup spans multiple actions or machines and later actions must wait for earlier ones—for example, renaming a Windows server, rebooting it, promoting it to a domain controller, then joining a workstation to that domain.

For one independent script, use `organesson_guest_setup` instead. A workflow is a provider-side OpenTofu resource; its definition is a separate TOML file and its scripts/assets live in directories alongside the configuration.

## Minimal layout

```text
my-project/
├── setup.tf
├── workflow/
│   └── setup.toml
└── artifacts/
    ├── configure-server/
    │   ├── entry.sh
    │   └── app.conf
    └── verify-client/
        └── entry.sh
```

The HCL resource connects workflow target names to VM resource IDs. Its normal OpenTofu `depends_on` ensures the VM and its network attachments are ready before the workflow starts.

```hcl
resource "organesson_guest_workflow" "lab_setup" {
  definition_file = "${path.module}/workflow/setup.toml"
  source_root     = path.module

  virtual_machines = {
    server = organesson_virtual_machine.server.id
    client = organesson_virtual_machine.client.id
  }

  depends_on = [
    organesson_network_attachment.server_lan,
    organesson_network_attachment.client_lan
  ]

  timeouts {
    create = "90m"
  }
}
```

The TOML target names (`server`, `client`) must exactly match the keys in `virtual_machines`. The values should be Organesson VM resource IDs, normally the `.id` of an `organesson_virtual_machine` resource—not Proxmox VMIDs.

## Define steps

Each `[[steps]]` table is one artifact execution:

```toml
name = "Example service setup"

[[steps]]
name = "configure-server"
target = "server"
source_directory = "artifacts/configure-server"
entrypoint = "entry.sh"

[[steps]]
name = "verify-client"
target = "client"
source_directory = "artifacts/verify-client"
entrypoint = "entry.sh"
depends_on = ["configure-server"]
```

Fields:

- The top-level `name` describes the workflow and is required.
- Each step's `name` is a unique identifier; `target` selects a key from the HCL `virtual_machines` map.
- `source_directory` is a clean relative path below `source_root`; `entrypoint` is a clean relative path inside that directory.
- `depends_on` lists step names that must finish successfully first. It can express dependencies across different VMs.
- `wait_for_windows_first_boot = true` waits for unattended Windows first boot to finish before running the step. It is for Windows guests only.
- `reboot_after = true` asks Organesson to reboot the target after the script succeeds, then wait for QEMU Guest Agent to return. `reboot_timeout_seconds` optionally sets that wait, from 60 to 3600 seconds; the default is 900 seconds. A timeout is invalid unless `reboot_after` is enabled.

For example, make workstation domain join depend on both workstation rename and domain-controller readiness:

```toml
[[steps]]
name = "join-domain"
target = "client"
source_directory = "artifacts/join-domain"
entrypoint = "join.ps1"
depends_on = ["rename-client", "verify-domain-controller"]
reboot_after = true
reboot_timeout_seconds = 1200
```

TOML dependencies determine guest-action order. OpenTofu `depends_on` determines when the workflow resource can start relative to other Terraform resources; they solve different ordering problems. Workflow steps currently run one at a time in a stable topological order. Independent steps are not parallelized.

## Artifact and guest behavior

At plan/apply time, the provider packages each `source_directory` as a deterministic compressed archive. Entrypoints and file paths must stay inside that directory. Packages are limited to 128 regular files and 16 MiB uncompressed; symbolic links and special files are rejected. A source file with an executable bit is transferred as executable, though the Linux runner invokes the entrypoint through Bash. Windows entrypoints are PowerShell scripts.

During apply, the provider uploads the archive and its SHA-256 digest to Organesson. Organesson verifies the archive and digest, then uses QEMU Guest Agent to execute it on the managed VM. Linux runs as root, Windows runs as LocalSystem, and BSD runs through its system shell. Scripts therefore have full administrative authority in the guest. Keep workflow files trusted, protect the API token, and avoid checking lab passwords or production secrets into the repository.

The API token's account needs `guest.execute` on each target VM (or an ancestor group with descendant inheritance). If an external runner must discover VMs from the deployment API, also grant `resource.view` at the appropriate scope. `guest.execute` is intentionally separate from deployment configuration management. Guest execution and readiness operations may start a stopped VM; `reboot_after` explicitly restarts it.

The temporary guest workspace is cleaned after execution. The API returns stdout and stderr, each limited to 32 KiB; `output_truncated` indicates when either stream was shortened. Workflow resource state keeps a digest and summary, not artifact contents or guest output. A failed script produces a failed apply with the step and target named in diagnostics. Guest scripts run on every request, including when the same artifact is submitted again, so make scripts safe to retry. Transport retries are limited to transient QEMU Guest Agent unavailability.

`wait_for_windows_first_boot` can take several minutes while Windows completes OOBE and may span guest-initiated reboots. `reboot_after` is the explicit reboot mechanism for subsequent steps; scripts should not also independently reboot unless that is part of a deliberate, separately managed action.

## Change, retry, and destroy semantics

The provider fingerprints the TOML definition and all referenced artifacts. Editing a script or the definition changes the workflow digest and causes OpenTofu to replace/re-run the workflow resource. Workflow steps execute again on each workflow create. The workflow has a 60-minute create timeout by default; use the standard `timeouts.create` setting if the complete sequence needs longer. Run `tofu plan` and `tofu apply` after edits; if files change after planning, the apply is rejected and you should plan again.

Workflows are imperative guest operations represented in OpenTofu state. Destroying `organesson_guest_workflow` only removes that workflow's state record; it does not reverse configuration inside guests. Write scripts to be safe to retry where practical, and use explicit verification steps to catch partial setup. Destroy the VM resources separately if the guests themselves should be removed.

## Complete example

[`examples/example_ad_forest`](../examples/example_ad_forest/README.md) contains a working workflow and its per-step PowerShell artifacts. Its [TOML graph](../examples/example_ad_forest/workflow/forest.toml) shows first-boot waits, cross-VM prerequisites, reboots, and post-setup verification. Use it as the reference for a multi-machine workflow; do not copy its lab credentials into a real environment.
