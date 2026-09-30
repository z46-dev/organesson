# Provider smoke test

This is the first API-backed subset of the Alice/Bob/Charlie/Dave project. OpenTofu creates one deployment, a user group and logical lab for Charlie and Dave, a simulated Fedora VM per lab, and `resource.view` / `vm.power_control` group grants. No Proxmox resources are created yet.

The browser UI is the human-facing way to set up accounts and verify access. The only terminal-only steps are starting local services, seeding the explicitly development-only accounts, and running OpenTofu.

## Start a local instance

Create a local `provider-smoke.toml` (do not use this fixture setting on a shared instance):

```toml
[web_server]
address = "127.0.0.1:6800"

[database]
file = "/tmp/organesson-provider-smoke.db"

[development]
enable_test_fixtures = true
```

In one terminal from the repository root, start the backend and copy the one-time administrator setup token printed at startup:

```sh
go run ./backend --config ./provider-smoke.toml
```

In another terminal from the repository root, seed the four fixture identities. Save the activation tokens printed for Charlie and Dave:

```sh
go run ./backend --config ./provider-smoke.toml development seed-test-users
```

Start the Vite frontend in `frontend/` with `bun install --frozen-lockfile && bun dev`. Open `http://localhost:8080`. Vite proxies `/api` to `http://127.0.0.1:6800`; set `ORGANESSON_API_TARGET` if the backend listens elsewhere. The UI and API use same-origin browser cookies through this proxy.

## Set up accounts in the UI

1. On first launch, paste the administrator setup token into the activation form and choose a password of at least 12 characters. The UI signs the administrator in after setup.
2. Sign out, choose the one-time activation option, and redeem Charlie's activation token with a test password. Repeat for Dave. Then sign back in as `administrator@organesson`.
3. In the administrator dashboard, create a 30-day provider token. Copy it immediately; the UI only displays the raw token once. The UI keeps the new token's identifier for this session and offers a revoke button after OpenTofu is destroyed.

## Apply and verify with OpenTofu

Build the provider and prepare its local development override:

```sh
mkdir -p .bin
go build -o .bin/terraform-provider-organesson ./provider
cp examples/provider_smoke/tofu.rc.example examples/provider_smoke/tofu.rc
```

Replace `REPOSITORY_ROOT` in `examples/provider_smoke/tofu.rc` with the absolute repository path, then from the repository root:

```sh
export TF_CLI_CONFIG_FILE="$PWD/examples/provider_smoke/tofu.rc"
export ORGANESSON_ENDPOINT="http://127.0.0.1:6800"
export ORGANESSON_TOKEN="<token-copied-from-the-admin-dashboard>"
cd examples/provider_smoke
tofu plan
tofu apply
tofu plan
```

OpenTofu may tell you to skip `tofu init` because the development override loads the local provider binary. The second plan should report `No changes`.

Sign out of the admin account in the UI and sign in as Charlie. The deployment should show only `charlie-fedora`; Dave's VM should not appear. Charlie's VM power button should work. Sign in as Dave and confirm the inverse view. These are simulated power-state changes, not actual VM operations.

To verify permission revocation, return to the terminal and remove Charlie from the OpenTofu-managed group:

```sh
tofu apply -var='charlie_members=[]'
```

Back in the UI, sign in as Charlie and refresh deployments: no deployment should be visible. Dave should still see Dave's VM. Restore Charlie's membership with `tofu apply`, and run `tofu destroy` when finished. Sign back in as the administrator and revoke the provider token in the dashboard. Stop both local processes and remove the local database file.

## Current boundary

The UI currently supports local sign-in/activation, deployment and visible-resource browsing, simulated VM power actions, and one-time provider-token creation/revocation. Resource provisioning remains OpenTofu-driven. Networks, address pools, disks, and artifacts are still provider prototypes; Proxmox integration, UI-based group administration, and a browsable token list are future work.
