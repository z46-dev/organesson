# Organesson OpenTofu provider

This is the local-first provider prototype. It parses individual deployment resources, logs their intended actions, and writes one summary per resource to OpenTofu state. User identities are resolved by Organesson using identifiers such as `alice@organesson`; deployment-local groups and validated fixed permission grants are defined in OpenTofu. Its artifact resource accepts a source directory that the future provider will package and hash into an immutable artifact. It does not connect to Organesson or Proxmox and cannot create infrastructure yet.

Build the provider from the repository root:

```sh
mkdir -p .bin
go build -o .bin/terraform-provider-organesson ./provider
```

Copy `examples/test_project/tofu.rc.example` to `examples/test_project/tofu.rc`, replace `REPOSITORY_ROOT`, and run:

```sh
export TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc"
cd examples/test_project
TF_LOG=INFO tofu plan
TF_LOG=INFO tofu apply
```

The CLI configuration uses a development override, so OpenTofu loads the local provider binary rather than downloading one from a registry. Do not commit `tofu.rc`.
