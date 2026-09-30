# Organesson OpenTofu provider

The provider now connects to Organesson using a revocable, expiring bearer token. Deployment, logical-group, user-group membership, fixed permission-grant, and simulated-VM resources have API-backed create/read/update/delete behavior. The remaining network, address-pool, disk, and artifact resources are still local parse-only prototypes. No resource calls Proxmox yet.

For the first real `tofu plan` / `apply` / refresh / access-control smoke test, use [`examples/provider_smoke`](../examples/provider_smoke/README.md). It exercises Charlie/Dave isolation with fake users and changes membership in place. The full `examples/test_project` still includes resource types that are not wired to the API.

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
