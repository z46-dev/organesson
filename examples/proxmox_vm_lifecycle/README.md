# Proxmox VM lifecycle acceptance

The focused `examples/proxmox_vm_lifecycle` fixture creates one deployment and one Fedora VM. For the Alice/Bob/Charlie/Dave application scenario, run `examples/test_project` with `proxmox_lifecycle_smoke=true`: it creates the project records but clones only Charlie's `internet_fedora`; the other VMs are simulated. Neither path creates networks or customizes the guest. Dave is intentionally not granted access so the UI permission boundary can be checked.

## Before apply

1. Start with the clean Organesson database you want to use for the run. Configure the Proxmox API endpoint and token in `backend/config.toml`; a lab self-signed certificate can use `insecure_skip_verify = true`. Do not put this Proxmox token in the OpenTofu provider configuration. The backend configuration section is:

   ```toml
   [proxmox]
   api_url = "https://pve.example:8006/api2/json"
   api_token_id = "organesson@pve!organesson"
   api_token_secret = "<token-secret>"
   insecure_skip_verify = true
   ```
2. Start the backend from the repository root with `go run ./backend --config ./backend/config.toml`. Complete administrator setup using the one-time link printed in the console. In another terminal, run `cd frontend && bun install --frozen-lockfile && bun dev` and open `http://localhost:8080`.
3. If these fake identities do not exist yet, stop the backend, temporarily set `development.enable_test_fixtures = true`, and run `go run ./backend --config ./backend/config.toml development seed-test-users`. Save Charlie's and Dave's activation links, turn test fixtures back off, then restart the backend. Redeem the links in the UI and create an Organesson API token for OpenTofu in the admin page. Charlie will receive access from this configuration; Dave receives none.
4. Run the current `frontend/public/scripts/template-prep/linux/og-prep-linux.sh` as root inside the Fedora source VM before checking it. On SELinux systems this enables the QEMU Guest Agent execution transition and installs the labeled Organesson wrapper needed for system-level guest setup; the preflight now verifies execution outside the confined `virt_qemu_ga_t` domain, not only UID 0. In Administration → Source VMs, register the Fedora source VM and start it for **Check source**. Confirm the temporary provisioning account has been removed, then stop the source before apply if desired. The alias used by both examples is `og-template-fedora-server-latest`; if you choose another alias, update `template` in `machine.tf` and `machines.tf`. Keep the source as an ordinary editable VM, not a Proxmox template.
5. In Administration → Proxmox resources, allow the `organesson` pool and `laas` storage (or change `pool` and `storage` in the example to match your cluster policy), set any capacity limits, and validate/save. The source VM must be outside this Organesson deployment and must not be altered by this run.
6. Build the local provider and prepare its OpenTofu development override:

   ```sh
   mkdir -p .bin
   go build -o .bin/terraform-provider-organesson ./provider
   cp examples/test_project/tofu.rc.example examples/test_project/tofu.rc
   ```

   Replace `REPOSITORY_ROOT` in `examples/test_project/tofu.rc` with the absolute repository path. Then run from the repository root:

   ```sh
   export TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc"
   export ORGANESSON_ENDPOINT="http://127.0.0.1:6800"
   export ORGANESSON_TOKEN="<one-time-copied-organesson-api-token>"
   cd examples/test_project
   tofu validate
   tofu plan -var='proxmox_lifecycle_smoke=true'
   tofu apply -var='proxmox_lifecycle_smoke=true'
   tofu plan -var='proxmox_lifecycle_smoke=true'
   ```

   With a provider development override, skip `tofu init`; `tofu validate` and subsequent commands use the local provider binary directly.

The Proxmox API token needs read access for inventory/source inspection, guest-agent command access on source VMs, and permission to clone/configure the selected source into the allowed pool/storage, read the resulting VM, control its power, and delete that clone. Scope it to the lab resources and Organesson-managed VMs where possible.

## Acceptance checks

1. Confirm the apply output contains one Proxmox VMID and node. In PVE, verify exactly one new VM exists in the selected pool/storage, cloned from the chosen source, and that its description has the Organesson ownership marker. Confirm the source remains unchanged, the clone has one Organesson-owned `cyber.lab` NIC and no inherited installer ISO, and its boot order selects the primary disk. The guest should be running after QEMU Guest Agent network setup. Unrelated VMs must be unchanged; the other test-project VMs are simulated and must not appear in PVE.
2. Run `tofu plan -var='proxmox_lifecycle_smoke=true'` again. It must report `No changes`; `charlie_internet_fedora_proxmox_vmid` and `charlie_internet_fedora_proxmox_node` must remain stable.
3. Sign in to Organesson as `charlie@organesson`. The deployment and VM should be visible; start and stop it using the VM's power control. Confirm the UI's state matches PVE after refresh.
4. Sign in as `dave@organesson`. The deployment should not be visible, and a direct power request for Charlie's VM must return `403` without changing its PVE state.
5. Return to OpenTofu and run `tofu destroy -var='proxmox_lifecycle_smoke=true'`. Confirm the managed guest profile, NIC, and VM are gone and the source and unrelated PVE resources remain unchanged.

Keep the OpenTofu state and API token private. Revoke the token after the run, stop local services, and remove only the isolated database/configuration files created for this acceptance test.
