# Organesson OpenTofu provider

The provider connects to Organesson using a revocable, expiring bearer token. Deployment, ownership/group/permission, VM, address-pool, SDN network, NIC attachment, and guest IPv4 configuration resources have API-backed lifecycle behavior. Proxmox VM, SDN, NIC, and guest operations are performed by Organesson after ownership and platform-policy checks; guest networking uses a short-lived QEMU Guest Agent script and a MAC-bound NetworkManager profile. Virtual-disk and artifact resources remain local modeling declarations in this slice.

Use [`examples/provider_smoke`](../examples/provider_smoke/README.md) for the simulated Charlie/Dave API and access-control path. For one real clone within the Alice/Bob/Charlie/Dave configuration, set `proxmox_lifecycle_smoke=true` in [`examples/test_project`](../examples/test_project/README.md); that keeps only Charlie's internet Fedora VM Proxmox-backed while the rest remain simulated. The separate [`examples/proxmox_vm_lifecycle`](../examples/proxmox_vm_lifecycle/README.md) guide has a minimal live-lab checklist.

Build the provider from the repository root:

```sh
mkdir -p .bin
go build -o .bin/terraform-provider-organesson ./provider
```

Copy `examples/provider_smoke/tofu.rc.example` to `examples/provider_smoke/tofu.rc`, replace `REPOSITORY_ROOT`, and run:

```sh
export TF_CLI_CONFIG_FILE="$PWD/examples/provider_smoke/tofu.rc"
export ORGANESSON_ENDPOINT="https://organesson.example"
export ORGANESSON_TOKEN="<token-from-your-organesson-account>"
cd examples/provider_smoke
tofu plan
tofu apply
tofu plan
```

The CLI configuration uses a development override, so OpenTofu loads the local provider binary rather than downloading one from a registry. Do not commit `tofu.rc` or place bearer tokens in `.tf` files or state.
