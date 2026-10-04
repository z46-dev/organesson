# Test project

This is the Alice/Bob/Charlie/Dave ownership and access scenario from [`docs/usecases/user_experience.md`](../../docs/usecases/user_experience.md). OpenTofu defines deployment-local groups, ownership, and fixed permission grants. `@organesson` identities are supplied by Organesson's local test identity source.

## Proxmox topology

The default keeps virtual machines simulated. Set `proxmox_test_deployment=true` to create the full topology on Proxmox. All created VMs use pool `organesson` and storage `laas`.

Each student owns `f1`, `f2`, and `f3`, plus a private managed VNet and its router. Each `f3` connects to the single shared managed VNet and its single shared router. The shared router has the only router WAN attachment; it uses a reserved static address from the same validated `cyber.lab` request as the students' two `f1` VMs. The request is therefore three IPv4 addresses by default. Each `f1` uses its own reserved address on `cyber.lab`.

Every student-private VNet uses `192.168.2.0/24`, gateway/router address `.1`, a reservable `.2–.10` range, and configurable DHCP/DNS. Each student's three VMs each have three NICs on that VNet: a distinct address leased from `.2–.10`, a manually assigned `.20`, `.21`, or `.22`, and a DHCP interface. The private DHCP NIC is marked `never-default` so it does not compete with the intended egress NIC. Each student's `f1` and `f3` also share an unmanaged L2 network with static addresses `192.168.3.1/24` and `.2/24`.

The shared router advertises DHCP on `192.168.1.30–.40` and provides egress to `cyber.lab` by default. Its DHCP range, advertised DNS, WAN enablement, and WAN DHCP/static method are configurable. Private router DHCP ranges and DNS are configurable separately. Setting router egress to DHCP avoids consuming an extra cyber address; static egress allocates one, so the default request count is `number of students + 1`.

Routers use the reusable `organesson_router` provider resource. It reads the LAN/WAN NIC MAC addresses from their Organesson attachments, packages the generic setup payload locally, and executes it through QEMU Guest Agent. No router implementation script lives in this example. The Debian source VM must include NetworkManager, `dnsmasq`, `nftables`, Python 3, and the QEMU Guest Agent; see [the router source VM guide](../../docs/router/debian-router-source-vm.md). Router Polling reads DHCP leases and neighbor data through QGA. Proxmox `ogvxlan` remains administrator-owned: Organesson creates marked VNets but does not alter the zone or create subnet/IPAM records.

Before applying, validate platform policy with resource pool `organesson`, storage `laas`, enough capacity for 15 vCPU / 30 GiB RAM / 240 GiB boot disks, the cyber.lab address pool, and `ogvxlan` as the VNet source. The deployment creates five VNets (one shared, two private, two f1↔f3 links). Ensure their `192.168.1.0/24`, `192.168.2.0/24`, and `192.168.3.0/24` ranges do not overlap other connected networks.

## Deploy and verify

Build the local provider and set the API endpoint, CA certificate (if using the development certificate), and `ORGANESSON_TOKEN`. From the repository root:

This replaces the previous two-VM-per-student layout. If the existing test deployment is still in OpenTofu state, the plan will remove its old VMs and networks and create the new topology. Review that plan carefully; a clean redeploy requires an intentional destroy followed by an apply. This change has only been validated locally and has **not** been applied to the live Proxmox deployment.

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project validate
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_test_deployment=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project apply -var='proxmox_test_deployment=true'
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" tofu -chdir=examples/test_project plan -var='proxmox_test_deployment=true'
```

The final plan should be empty. Test each user's visibility and permissions in the UI. To check cross-node L2, migrate one student VM to another node in the `ogvxlan` zone, then confirm it retains its DHCP/static connectivity and that a plan remains empty. Destroy with the same `proxmox_test_deployment=true` value. Keep provider tokens and state private.
