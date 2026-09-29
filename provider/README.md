# Organesson OpenTofu provider

This is the local-first provider prototype. It accepts a Team LAN deployment declaration, expands it into logged actions, and writes the same action list to OpenTofu state. It does not connect to Organesson or Proxmox and cannot create infrastructure yet.

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
