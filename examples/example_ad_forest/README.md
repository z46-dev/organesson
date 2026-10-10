# Example AD forest

This OpenTofu example creates one IPv4 managed VNet with static egress through the platform `cyber` network, a Windows Server 2025 domain controller, and a Windows 11 workstation. It reserves one cyber-network IPv4 address for the managed router, installs a new `adforest.test` forest, creates a test AD account, joins the workstation, then verifies the domain secure channel and account credentials through QEMU Guest Agent.

Both VMs use the ready Windows templates `og-template-windows-server-2025` and `og-template-windows-11-pro-workstation`. The Windows clones use `e1000e`, matching the adapter driver already present in these templates. The source VMs are not changed. VMs and the managed-network router are placed in Proxmox pool `organesson` and storage `laas`.

The server has the reserved address `192.168.77.10/24`; the managed router is `192.168.77.1`. DHCP serves `.30`–`.40` and advertises the new domain controller as DNS. The server is renamed to `OG-AD-DC01` before AD DS promotion so workstation provisioning can address it reliably. `setup.tf` starts one Organesson guest workflow after both NICs are attached. The separate `workflow/forest.toml` names each action, its VM target, its prerequisites, and whether that action reboots. Organesson executes scripts through QEMU Guest Agent and waits for the agent to disappear and return after expected reboots. The workflow then waits for the AD computer account and DNS record, checks `Test-ComputerSecureChannel`, and authenticates the AD test user. There are no fixed sleep steps or manual post-apply setup commands.

The example deliberately uses `password123!` for the workstation-local built-in `Administrator`, domain administrator `ADFOREST\Administrator`, the DSRM password, and standard domain user `ADFOREST\labuser`. The temporary `ogadmin` account from the template answer file is removed from both clones after unattended first boot. `ADFOREST\Administrator` is the Domain Admin and authenticates on both machines. `labuser` is a standard domain user intended for workstation sign-in, not an administrator; ordinary Domain Users do not have interactive sign-in rights on a domain controller by default. These are lab-only credentials; use only on an isolated development system. PowerShell setup logs remain on the guests under `C:\ProgramData\Organesson\example-ad-forest`.

## Run

Before applying, Administration → Quotas & placement must have a managed-network VNet source and router template/pool/storage configured. The source aliases must exist and be ready. Windows source preparation seals an answer file into each template so cloned guests skip interactive OOBE and get an automation administrator. The first workflow step waits for Windows to finish its unattended OOBE pass and exit `msoobe.exe` before changing credentials or removing the one-time answer file; this avoids racing guest-agent availability against Windows setup. The workflow then owns each expected restart, verifies the domain controller is not in OOBE, and joins the workstation. After provisioning, use these lab logins:

- Workstation local administrator: `OG-WIN11-01\Administrator` / `password123!`
- Domain administrator (controller and workstation): `ADFOREST\Administrator` / `password123!`
- Standard domain user (workstation): `ADFOREST\labuser` or `labuser@adforest.test` / `password123!`

The local account is for workstation recovery; domain accounts are the normal test logins. This is intentionally a development-only setup.

Build the provider and point the local CLI override at the repository:

```bash
go build -o .bin/terraform-provider-organesson ./provider
cp examples/example_ad_forest/tofu.rc.example examples/example_ad_forest/tofu.rc
export TF_CLI_CONFIG_FILE="$PWD/examples/example_ad_forest/tofu.rc"
export ORGANESSON_ENDPOINT="https://petropavlovsk.cyber.lab:6800"
export ORGANESSON_CA_CERT="$PWD/backend/tls/server.crt"
export ORGANESSON_TOKEN="<admin API token>"
cd examples/example_ad_forest
```

The Proxmox VM pool must remain `organesson` and VM disk storage must remain `laas`. Apply normally; Organesson runs the ordered workflow and waits until every step succeeds:

```bash
tofu plan
tofu apply
```

With a provider development override, skip `tofu init`; it attempts registry discovery even though the local binary is being used. The apply is complete only after `organesson_guest_workflow.forest` reports success. If a later step fails, rerunning `tofu apply` is safe: already successful guest artifacts are recognized by digest and skipped, while incomplete steps can retry. In the Organesson UI, select the `example-ad-forest` deployment to inspect both VMs and its managed network. To remove the test, run `tofu destroy` from this directory; that tears down only resources tracked by this example.
