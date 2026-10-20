# Organesson OpenTofu provider

The provider connects to Organesson using a revocable, expiring bearer token. Deployment, ownership/group/permission, VM, address-pool, SDN network, NIC, guest IPv4, and guest artifact setup resources use the Organesson API. Proxmox and privileged guest operations are performed by Organesson after ownership and platform-policy checks. Artifact archives are bounded, validated, and sent only during apply. Linux guests run from a temporary `/run` workspace; Windows guests run PowerShell as LocalSystem from a temporary Windows directory. The workspace is removed after execution, and only digest/result metadata are retained. See [Guest workflows](WORKFLOWS.md) for authoring and execution details for `organesson_guest_workflow`; the complete [AD forest example](../examples/example_ad_forest/README.md) demonstrates a multi-VM setup. `organesson_network_attachment.model` supports `virtio` (default) and `e1000e` for Windows templates without a VirtIO NIC driver. Virtual-disk lifecycle is not implemented yet.

Use [`examples/test_project`](../examples/test_project/README.md) for the Alice/Bob/Charlie/Dave scenario, [`examples/artifact_smoke`](../examples/artifact_smoke/README.md) as the Linux live acceptance fixture, and [`examples/example_ad_forest`](../examples/example_ad_forest/README.md) for Windows Server 2025 and Windows 11 guest setup. The reserved [`examples/it666_project`](../examples/it666_project/README.md) directory is intentionally blank until that scenario is ready to implement.

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
export ORGANESSON_CA_CERT="/path/to/organesson-ca.pem" # optional for private/self-signed HTTPS
cd examples/artifact_smoke
tofu plan
tofu apply
tofu plan
```

The CLI configuration uses a development override, so OpenTofu loads the local provider binary rather than downloading one from a registry. Do not commit `tofu.rc` or place bearer tokens in `.tf` files or state. Prefer installing the issuing CA or fixing the certificate SAN. As a last resort for isolated lab testing, `ORGANESSON_INSECURE_SKIP_VERIFY=true` disables TLS certificate and hostname verification and can expose the bearer token to interception; do not use it on untrusted networks.
