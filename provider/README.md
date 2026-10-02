# Organesson OpenTofu provider

The provider connects to Organesson using a revocable, expiring bearer token. Deployment, ownership/group/permission, VM, address-pool, SDN network, NIC, guest IPv4, and guest artifact setup resources use the Organesson API. Proxmox and root guest operations are performed by Organesson after ownership and platform-policy checks. Artifact archives are bounded, validated, sent only during apply, streamed to `/run` through QEMU Guest Agent, and removed after execution; only their digest/result metadata are retained. Virtual-disk lifecycle is not implemented yet.

Use [`examples/test_project`](../examples/test_project/README.md) for the Alice/Bob/Charlie/Dave scenario and [`examples/artifact_smoke`](../examples/artifact_smoke/README.md) as the small live acceptance fixture for the current implementation. The reserved [`examples/it666_project`](../examples/it666_project/README.md) directory is intentionally blank until that scenario is ready to implement.

Build the provider from the repository root:

```sh
mkdir -p .bin
go build -o .bin/terraform-provider-organesson ./provider
```

Copy `examples/artifact_smoke/tofu.rc.example` to `examples/artifact_smoke/tofu.rc`, replace `REPOSITORY_ROOT`, and run:

```sh
export TF_CLI_CONFIG_FILE="$PWD/examples/artifact_smoke/tofu.rc"
export ORGANESSON_ENDPOINT="https://organesson.example"
export ORGANESSON_TOKEN="<token-from-your-organesson-account>"
cd examples/artifact_smoke
tofu plan
tofu apply
tofu plan
```

The CLI configuration uses a development override, so OpenTofu loads the local provider binary rather than downloading one from a registry. Do not commit `tofu.rc` or place bearer tokens in `.tf` files or state.
