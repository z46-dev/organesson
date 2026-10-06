# Test project

This is the Alice/Bob/Charlie/Dave ownership and access scenario from [`docs/usecases/user_experience.md`](../../docs/usecases/user_experience.md). OpenTofu defines deployment-local groups, ownership, and fixed permission grants. `@organesson` identities are supplied by Organesson's local test identity source.

## Proxmox topology

This example always provisions the full topology on Proxmox. All created VMs use pool `organesson` and storage `laas`.

Each student owns `f1`, `f2`, and `f3`, plus a private managed VNet. Each `f3` connects to the single shared managed VNet. The platform provisions one internal router per managed VNet; those VMs and their interfaces are not in the student ownership tree or the project's top-level resource declarations. The shared network's router has the only router WAN attachment; it uses a reserved static address from the same validated `cyber.lab` request as the students' two `f1` VMs. The request is therefore three IPv4 addresses by default. Each `f1` uses its own reserved address on `cyber.lab`.

Every student-private VNet uses `192.168.2.0/24` and `fd42:2::/64`, with router addresses `.1` and `::1`. Each student has one IPv4 reservation request for three addresses in `.2–.10`, used by the reserved-address NIC on `f1`, `f2`, and `f3`. The other private-network addresses are ordinary interface configuration: static IPv4 `.20`, `.21`, and `.22`, static IPv6 `::20`, `::21`, and `::22`, plus IPv4/IPv6 DHCP. Private-router DHCPv4 defaults to `.30–.40` and DHCPv6 to `::30–::40`; both ranges are configurable. Those static values and DHCP leases are not address-pool request resources. The private DHCP NIC is marked `never-default` so it does not compete with the intended egress NIC. Each student's `f1` and `f3` also share an unmanaged L2 network with static addresses `192.168.3.1/24` and `.2/24`.

The shared router advertises DHCP on `192.168.1.30–.40` and provides egress to `cyber.lab` by default. Its DHCP range, advertised DNS, WAN enablement, and WAN DHCP/static method are configurable. `f3` uses DHCPv4 and a directly configured static IPv6 address on the shared VNet. Private router DHCP ranges and DNS are configurable separately. Explicit reservations bind their allocated address to the interface MAC and use an exact Proxmox firewall filter. Other managed-VNet interfaces use that VNet's configured subnet as their filter scope; DHCP leases still come from the router's configured DHCP range. Environment-network interfaces always require an address-pool request; DHCP egress reserves one address and fails closed if the upstream server leases a different address. The default cyber request count is `number of students + 1` when shared egress is enabled. Address-pool resources in this project are limited to the cyber request and one `.2–.10` reservation request per student.

Each managed VNet is one `organesson_managed_network` resource. Organesson owns the hidden router VM, its interfaces and guest IP configuration, DHCP/DNS, and optional egress as part of that resource. Router VMs stay in an internal ownership branch, hidden from deployment users but visible to platform administrators. Student NICs and guest IP configuration are declared from `locals.tf`. The shared VNet is exposed on VLAN 2048 through the authorized `tungsten:ogtrunk` trunk; because both nodes have the same authorized bridge name and VLAN, Proxmox makes that VNet available on both nodes through its VLAN SDN zone. Private VNets and the unmanaged point-to-point networks continue to use the administrator-owned `ogvxlan` source zone. Organesson does not edit either trunk or the imported VXLAN zone, and does not create SDN subnet/IPAM records. The Debian source VM must include NetworkManager, `dnsmasq`, `nftables`, Python 3, and the QEMU Guest Agent; see [the router source VM guide](../../docs/router/debian-router-source-vm.md). Router Polling reads DHCP leases and neighbor data through QGA.

Before applying, validate platform policy with resource pool `organesson`, storage `laas`, enough capacity for 15 vCPU / 30 GiB RAM / 240 GiB boot disks, the cyber.lab address pool, and `ogvxlan` as the VNet source. The deployment creates five VNets (one shared, two private, two f1↔f3 links). Ensure their `192.168.1.0/24`, `192.168.2.0/24`, and `192.168.3.0/24` ranges do not overlap other connected networks.

## Deploy and verify

From the repository root, build the local provider:

```sh
(cd provider && go build -o ../.bin/terraform-provider-organesson .)
```

In a separate terminal, start the backend:

```sh
(cd backend && go run . --config config.toml)
```

Sign in as a platform administrator, open **Administration → Provider access**, create a token, and copy it. In the deployment terminal, set the local API settings and token, then run the deployment. The first apply creates the complete live test deployment. Keep the provider token and state private.

```sh
export ORGANESSON_ENDPOINT="https://localhost:6800"
export ORGANESSON_CA_CERT="$PWD/backend/tls/server.crt"
export ORGANESSON_TOKEN="paste-admin-api-token-here"
export TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc"
tofu -chdir=examples/test_project validate
tofu -chdir=examples/test_project plan
tofu -chdir=examples/test_project apply
tofu -chdir=examples/test_project plan
```

The final plan should be empty. Test each user's visibility and permissions in the UI. To check cross-node L2, migrate one student VM to another node in the `ogvxlan` zone, then confirm it retains its DHCP/static connectivity and that a plan remains empty. Destroy with `tofu -chdir=examples/test_project destroy`. Keep provider tokens and state private.
