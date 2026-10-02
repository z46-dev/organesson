# Artifact delivery smoke test

This is the small, real-Proxmox fixture used to develop artifact delivery. It creates one Fedora VM for Charlie, packages a local directory as deterministic `tar.gz`, and during apply sends it through Organesson for root execution via QEMU Guest Agent. The entrypoint reads a second packaged file so the smoke checks more than just process launch. The archive is not stored in OpenTofu state or on the backend; the guest uses a unique temporary `/run` workspace.

The current packager allows at most 128 regular files and 16 MiB uncompressed, preserves executable bits, and rejects symlinks and unsafe or missing entrypoints. Artifact bytes are discarded by the provider and are not stored in OpenTofu state.

## Moving-goalpost acceptance

- OpenTofu packages the source reproducibly and exposes a SHA-256 digest; changing a source file changes the digest on refresh.
- Apply sends the package through the Organesson/Proxmox management path without a guest-side download and runs the entrypoint as guest root through QEMU Guest Agent.
- The API reports the guest exit status; a successful run is visible in apply, and the second plan is clean.
- Temporary guest files are removed after success and failure. Destroy removes the managed VM and leaves its source VM untouched.
- Tests cover unsafe paths, symlinks, size limits, interrupted execution, permission denial, and cleanup.

Live acceptance was completed on 2026-10-02 using Fedora Server source VM 157 and policy-approved pool `organesson` / storage `laas`. Apply cloned VM 129 on node `osmium`; the two-file payload ran successfully with exit code 0, a follow-up plan was clean, and a separate entrypoint exiting 7 returned `status=failed, exit_code=7`. Read-only QGA checks found no temporary artifact workspace after either run. Destroy removed VM 129 while source VM 157 remained. The test deployment and local state are now empty; test identity accounts were retained.

For another run, use an activated administrator, a ready Fedora Server source alias, and a validated Proxmox policy allowing pool `organesson` and storage `laas`. Follow the current source/policy setup guidance in [`docs/proxmox-vm-lifecycle.md`](../../docs/proxmox-vm-lifecycle.md), then build the local provider and configure a private `tofu.rc` from `tofu.rc.example`.

From the repository root:

```sh
go build -o .bin/terraform-provider-organesson ./provider
cp examples/artifact_smoke/tofu.rc.example examples/artifact_smoke/tofu.rc
```

Replace `REPOSITORY_ROOT` in `tofu.rc`; set `TF_CLI_CONFIG_FILE`, `ORGANESSON_ENDPOINT`, and `ORGANESSON_TOKEN` in the shell. Then:

```sh
tofu -chdir=examples/artifact_smoke validate
tofu -chdir=examples/artifact_smoke plan
tofu -chdir=examples/artifact_smoke apply
tofu -chdir=examples/artifact_smoke plan
tofu -chdir=examples/artifact_smoke destroy
```

Keep the token, local override, and OpenTofu state private. Revoke temporary tokens and destroy the smoke VM after each test.
