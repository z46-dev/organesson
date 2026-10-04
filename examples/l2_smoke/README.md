# VXLAN L2 smoke test

This small deployment checks that an Organesson-managed VNet built on the configured Proxmox VXLAN source carries guest traffic across nodes. It uses two Fedora Server clones with static addresses and no gateway, DHCP, or uplink.

## Prerequisites

- The administrator has validated Quotas & placement with `organesson` and `laas` selected and `ogvxlan` selected as the VNet source.
- The Fedora Server template alias `og-template-fedora-server-latest` is ready and supports QEMU Guest Agent.
- The provider binary is built in the repository's `.bin` directory and the backend is running.
- `ORGANESSON_TOKEN` and `ORGANESSON_ENDPOINT` are set in the shell.

Copy `tofu.rc.example` to `tofu.rc`, replacing `REPOSITORY_ROOT` with the absolute path to this checkout. Set the provider connection and run:

```sh
export TF_CLI_CONFIG_FILE="$PWD/tofu.rc"
export ORGANESSON_ENDPOINT="https://127.0.0.1:6800"
export ORGANESSON_CA_CERT="/absolute/path/to/organesson/backend/tls/server.crt"
# Set ORGANESSON_TOKEN in the shell; do not put it in a file or Terraform configuration.
tofu validate
tofu plan -var='provisioning_mode=proxmox'
tofu apply -var='provisioning_mode=proxmox'
tofu output
```

The provider development override loads the local binary directly, so `tofu init` is not needed for this development fixture.

Both VMs should initially land on the Fedora template's node. Confirm both addresses are configured in their consoles:

```sh
ip -4 address
ping -c 5 192.168.250.12 # from l2-smoke-a
ping -c 5 192.168.250.11 # from l2-smoke-b
```

The Fedora images include `ping`. When running diagnostics through QEMU Guest Agent, invoke commands through `/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec` so they use the SELinux domain enabled by the template-prep script. Commands entered in the VM console can be run normally.

After same-node ping succeeds, migrate `l2-smoke-b` to the other cluster node using the Proxmox UI (or the cluster's normal migration workflow). Do not move it outside the Organesson pool. Wait for migration to complete, then repeat both pings. Successful ping after migration demonstrates that the VNet is carried over the shared VXLAN source rather than relying on node-local networking.

Destroy the smoke resources when finished:

```sh
tofu destroy -var='provisioning_mode=proxmox'
```

Destroy removes only the two managed VMs, their marked NICs, the generated Organesson VNet, and the deployment records. The imported `ogvxlan` zone is not modified or deleted.
