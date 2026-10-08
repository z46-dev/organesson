# Debian 13 router source VM

This guide builds an ordinary, editable Proxmox VM that Organesson can clone as a small IPv4 router. The source contains the operating system and required tools, but no deployment addresses, active DHCP service, or enabled routing. A clone receives those settings for one deployment.

The first version uses NetworkManager for NIC configuration, `nftables` for firewalling/NAT, and `dnsmasq` for DHCP plus local/forwarding DNS. Organesson's guest-network operation configures NICs by MAC address. The OpenTofu `organesson_managed_network` resource creates the platform-owned router and packages its DHCP range and advertised DNS settings for execution through QEMU Guest Agent; it enables forwarding/NAT only when a WAN attachment is declared. The example project contains no router implementation script.

```text
approved environment network (WAN) ── router clone ── Organesson VNet (LAN)
       reserved IPv4 address       static gateway       DHCP + DNS
```

## Values to decide before building a clone

Do not bake these values into the source VM. They are inputs for each cloned router:

| Input | Source of truth |
| --- | --- |
| WAN Proxmox network | Administrator-approved environment network, normally `cyber.lab` / `vmbr0` in this lab |
| WAN IPv4 address and prefix | Reserve an address through Organesson's configured pool. Keep the environment prefix; the current lab uses `/8`, not a subnet carved from the pool. |
| WAN gateway and DNS | The selected environment network policy. Current lab values are gateway `10.0.0.1` and DNS `10.0.0.2`, `10.0.0.3`. |
| LAN VNet and subnet | The deployment's Organesson-managed VNet and its allocated IPv4 subnet. Select `ogvxlan` as the VNet source in **Administration → Quotas & placement**. |
| LAN gateway | Reserve an address in the subnet for the router, conventionally the first usable address such as `.1`; set the same gateway in the VNet subnet metadata. |
| DHCP range | A range inside the LAN subnet that excludes the router, network/broadcast addresses, static guests, and addresses allocated by Organesson. For this manual prototype, ensure DHCP addresses are excluded from any Organesson allocation pool yourself. |
| Egress | Explicitly decide whether LAN clients may reach the Internet. Default to isolated; do not infer Internet access from the fact that WAN is attached to `vmbr0`. |
| LAN DNS domain | A deployment-specific internal domain, for example `team-a.test`. `.test` is reserved for testing; do not use `.local`. |

The exact LAN subnet, DHCP range, and available egress must be agreed with the platform administrator before cloning. Do not copy the smoke test addresses unless they have been assigned to this deployment.

## 1. Create the source VM in Proxmox

Create a normal VM named `og-router-debian13-source`.

Suggested starting hardware:

| Setting | Value |
| --- | --- |
| BIOS / machine type | Keep the cluster's normal x86-64 defaults; use the same settings for later clones |
| CPU | 1 socket, 2 cores |
| Memory | 2048 MiB, fixed for the initial test |
| Disk | 16 GiB, SCSI on VirtIO SCSI single |
| Network device | VirtIO; attach one temporary maintenance NIC to the approved network for install/update only |
| QEMU Guest Agent | Enable the Proxmox VM option |
| ISO | Debian 13.7 netinst from the cluster's ISO storage |

The temporary maintenance NIC is for installing packages and downloading updates. Use the approved DHCP/maintenance path, do not assign an unreserved static address, and remove that NIC from the source VM after installation. The final source should have no deployment NICs; Organesson adds the WAN and LAN NICs to each clone.

## 2. Install Debian 13.7

Boot the VM from the Debian 13.7 netinst ISO and choose **Graphical install** (the text installer is also fine). Use these choices:

1. Select the normal language, location, and keyboard for the operators who maintain the source.
2. Let the installer configure the temporary maintenance NIC with DHCP. Use hostname `og-router-source`; leave the DNS domain blank unless your administrator has a specific source-VM domain to use.
3. Use the Debian mirror so the installer can fetch current packages. If that network has no mirror access, use an approved local mirror or full installation media; do not add an unapproved route.
4. Use guided partitioning on the VM's 16 GiB virtual disk, with one filesystem layout. This is a small appliance source, not a general-purpose multi-user server.
5. Leave the root-password field blank. Debian will disable password login for root and create the first user with `sudo` access. Create a source-maintenance account named `template-maint` with a unique, strong password. Keep it in a password manager; never put the password in this document or the repository. Debian documents this installer behavior in its [installation guide](https://www.debian.org/releases/trixie/amd64/ch06s03.en.html).
6. At software selection, leave **Debian desktop environment**, **SSH server**, and **web server** unchecked. Keep **standard system utilities** selected. This source is maintained through the Proxmox console, not by exposing SSH or a web admin service.
7. Install GRUB to the VM's primary virtual disk, finish installation, and reboot from the disk.

Log in through the Proxmox console as `template-maint`. Do not reuse the source account's password for any deployment user.

## 3. Install the router packages and make networking clone-safe

Organesson's Linux prep script installs and enables NetworkManager, which the per-NIC guest configuration uses through `nmcli`. Configure NetworkManager not to invent a DHCP connection for a newly attached NIC. This prevents a cloned WAN or LAN interface from coming up with an accidental/default configuration; `no-auto-default=*` is the NetworkManager-supported setting for this. The prep script applies this setting to a dedicated configuration file. See the [NetworkManager configuration reference](https://networkmanager.pages.freedesktop.org/NetworkManager/NetworkManager/NetworkManager.conf.html).

From the Debian console:

```sh
sudo install -d -m 0755 /etc/NetworkManager/conf.d
sudo tee /etc/NetworkManager/conf.d/10-organesson-router.conf >/dev/null <<'EOF'
[main]
no-auto-default=*
EOF

sudo apt-get update
sudo apt-get install --yes network-manager dnsmasq nftables python3 iputils-ping tcpdump curl ca-certificates
```

Do not put an IP address, gateway, DNS server, DHCP range, or active LAN config in the source. Disable router services on the source so it cannot accidentally answer DHCP or forward traffic while being maintained:

```sh
sudo systemctl disable --now dnsmasq.service nftables.service
```

The service configuration is applied to a **clone** after its Organesson network and NICs exist. `dnsmasq` is a good fit for the first small LAN because it provides DHCP and DNS together and can register DHCP client names in DNS ([dnsmasq manual](https://dnsmasq.org/docs/dnsmasq-man.html)).

## 4. Run Organesson's Linux prep script last

The prep script updates Debian, installs/enables QEMU Guest Agent, verifies that the agent has the required privileged execution path, clears clone machine identity, and tells you to shut down without rebooting. It does **not** install or configure the router services above, and it does not remove `template-maint`.

From the Debian console, download the script from the frontend using the actual Organesson frontend hostname and port in place of `ORGANESSON_FRONTEND_HOST:PORT`:

```sh
curl --fail --insecure --location --output og-prep-linux.sh \
  'http://ORGANESSON_FRONTEND_HOST:PORT/scripts/template-prep/linux/og-prep-linux.sh'
sudo bash ./og-prep-linux.sh
```

Use HTTPS instead if that is how your frontend is served. Do not pipe the download directly to a shell: review the script and keep it on disk until it exits successfully. The script leaves Debian's AppArmor policy unchanged; if it reports that QEMU Guest Agent is confined, stop and resolve that before registering the source. Do not weaken AppArmor to make the check pass.

After a successful run, make the install-time NIC configuration loopback-only so it cannot follow a clone. This is done from the local console; losing the temporary network at this point is expected. If the `networking.service` unit exists, disable it; Debian installs that unit with `ifupdown`:

```sh
sudo tee /etc/network/interfaces >/dev/null <<'EOF'
auto lo
iface lo inet loopback
EOF

if systemctl cat networking.service >/dev/null 2>&1; then
  sudo systemctl disable --now networking.service
fi
sudo systemctl enable NetworkManager.service
sudo systemctl restart NetworkManager.service
sudo nmcli --fields NAME,TYPE,AUTOCONNECT connection show
```

There must not be an auto-connecting `Wired connection` profile left over from package installation. If one exists, inspect and delete that specific temporary profile with `sudo nmcli connection delete 'PROFILE_NAME'`. Do not delete profiles by a broad wildcard.

Test the QEMU Guest Agent from a Proxmox node while the source is still running. Debian normally uses an unconfined AppArmor path, so the direct root command should work:

```sh
qm guest exec SOURCE_VMID -- /usr/bin/id -u
```

The command result must be `0`. If it is denied, do not mark the source ready; use the error and the prep-script result to diagnose the actual policy. Then remove the downloaded script and shut down without rebooting:

```sh
sudo rm -- ./og-prep-linux.sh
sudo shutdown -h now
```

Do not boot the source again before cloning. A reboot regenerates the cleared machine identity, and the prep script would need to be run again before a clone is made.

## 5. Seal the Proxmox source configuration

Once the guest is fully shut down:

1. Remove the Debian ISO from the CD/DVD device.
2. Remove the temporary maintenance NIC from the source VM's Hardware page. Keep the QEMU Guest Agent option enabled.
3. Confirm boot order points to the installed disk and the VM remains an ordinary powered-off VM. **Do not convert it to a Proxmox template**; Organesson source VMs stay editable so administrators can update them later.
4. Register its VMID in Organesson's source-VM catalog under an alias such as `og-template-debian-router-13-latest`. Mark it ready only after a disposable clone passes the checks below.

The source retains the `template-maint` account for updates. Each router clone also inherits it, so for now remove it from a clone before attaching student workloads or marking that router ready. Log the account out first, then use a trusted QEMU Guest Agent command or the Proxmox console to run:

```sh
/usr/sbin/userdel --remove template-maint
```

Do not remove the source account from the source VM; it is needed for future maintenance. A future Organesson router workflow should make clone-account replacement/removal explicit and automated.

## 6. Configure and test one disposable router clone

The current `examples/test_project` provisions and configures its router through OpenTofu. The manual commands in this section are retained for inspecting a disposable source-image clone; do not use them alongside an active Terraform-managed deployment. Keep any manual clone in pool `organesson` with its disk on `laas`, and keep the source powered off. On that disposable clone, attach:

- `net0` to the approved WAN network (normally `vmbr0` in this lab), with one address reserved through the Organesson environment address pool; and
- `net1` to a newly created Organesson VNet backed by `ogvxlan`, with a managed subnet and the router's LAN address recorded as the subnet gateway.

Keep the VNet's Proxmox DHCP option off: this router clone is the one DHCP server for the LAN. Do not change the `ogvxlan` zone itself. Start the clone, log in at the Proxmox console, and identify the actual interface names and MAC addresses:

```sh
ip -br link
nmcli --fields GENERAL.DEVICE,GENERAL.HWADDR device show
```

### LAN-only test with no egress

The Terraform-managed example creates one shared router with `cyber.lab` egress by default, using a reserved static WAN address, DHCP range `192.168.1.30–.40`, and LAN DNS `192.168.1.1`. It performs NAT to public IPv4 destinations while blocking RFC1918 and link-local forwarding. Egress can be disabled or changed to DHCP through OpenTofu inputs. Every student-private VNet gets its own router with DHCP range `192.168.2.30–.40`, DNS `192.168.2.1`, and no WAN. The rest of this subsection describes a standalone manual LAN-only clone, so do **not** attach a WAN NIC or follow its manual-clone commands while testing the Terraform-managed deployment.

When recreating the example from empty state, first create only its shared VNet so the router can attach to it:

```sh
TF_CLI_CONFIG_FILE="$PWD/examples/test_project/tofu.rc" \
  tofu -chdir=examples/test_project apply \
  -target='organesson_network.shared_student_lan["shared"]' \
  -var='proxmox_test_deployment=true'
```

Read the generated VNet name with `tofu -chdir=examples/test_project state show 'organesson_network.shared_student_lan["shared"]'`. Full-clone source VM 106 into pool `organesson`, put its disk on `laas`, and attach its only NIC to that VNet. Configure the guest as described below, then run the ordinary full `tofu apply` for `proxmox_test_deployment=true`; the LAN guests' DHCP profiles need the router to be running first. The current test router is deliberately a manual Proxmox clone, not a resource in the OpenTofu state. Remove that clone before destroying the OpenTofu deployment, because it is still attached to the managed VNet.

Use this LAN-only guest setup instead of the WAN+LAN profile commands that follow. Run it as root on the router clone after replacing `LAN_MAC` with the MAC shown for its single Proxmox NIC:

```sh
LAN_MAC='BC:24:11:00:00:01'
LAN_IF='ens18'
LAN_PROFILE="organesson-$(printf '%s' "$LAN_MAC" | tr -d ':' | tr '[:upper:]' '[:lower:]')"

if nmcli -t -f NAME connection show | grep -Fxq "$LAN_PROFILE"; then
  nmcli connection modify "$LAN_PROFILE" connection.autoconnect yes \
    ethernet.mac-address "$LAN_MAC" ipv4.method manual \
    ipv4.addresses '192.168.1.1/24' ipv4.gateway '' ipv4.dns '' \
    ipv4.never-default yes ipv6.method disabled
else
  nmcli connection add type ethernet ifname "$LAN_IF" con-name "$LAN_PROFILE" \
    connection.autoconnect yes ethernet.mac-address "$LAN_MAC" \
    ipv4.method manual ipv4.addresses '192.168.1.1/24' \
    ipv4.never-default yes ipv6.method disabled
fi
nmcli connection up "$LAN_PROFILE"
```

Create `/etc/dnsmasq.d/organesson-router.conf` on the clone. Replace `LAN_IF` with the interface name resolved from its Proxmox attachment MAC; never assume a fixed device name such as `net0`:

```ini
interface=LAN_IF
except-interface=lo
listen-address=127.0.0.1,192.168.1.1
bind-dynamic
local-service
no-resolv
domain-needed
bogus-priv
domain=testproject.test
expand-hosts
local=/testproject.test/
dhcp-authoritative
dhcp-range=192.168.1.30,192.168.1.40,12h
dhcp-option=option:router,192.168.1.1
dhcp-option=option:dns-server,192.168.1.1
dhcp-host=CHARLIE_LAN_MAC,charlie-lan
dhcp-host=DAVE_LAN_MAC,dave-lan
```

Omit the last two `dhcp-host` lines if you do not want stable names. Create a restrictive nftables file with the LAN interface substituted. This permits DHCP, DNS, and ping to the router while denying forwarding and all other inbound traffic:

```nft
#!/usr/sbin/nft -f
flush ruleset
table inet organesson {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        ct state invalid drop
        ct state established,related accept
        iifname "LAN_IF" ip protocol icmp accept
        iifname "LAN_IF" udp dport { 53, 67 } accept
        iifname "LAN_IF" tcp dport 53 accept
    }
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
    chain output {
        type filter hook output priority filter; policy drop;
        oifname "lo" accept
        ct state invalid drop
        ct state established,related accept
        oifname "LAN_IF" ip protocol icmp accept
        oifname "LAN_IF" udp sport 67 udp dport 68 accept
        oifname "LAN_IF" udp sport 53 accept
        oifname "LAN_IF" tcp sport 53 accept
    }
}
```

Set forwarding off, validate both configs, and enable the services:

```sh
printf 'net.ipv4.ip_forward=0\n' > /etc/sysctl.d/90-organesson-router.conf
sysctl --system
dnsmasq --test
nft --check --file /etc/nftables.conf
systemctl enable --now nftables.service dnsmasq.service
systemctl --no-pager --full status nftables.service dnsmasq.service
```

Delete the inherited `administrator` account from the clone after logging it out; never delete it from source VM 106 as part of this test. This LAN-only variant is isolated: do not add a default route or WAN DNS server.

Correlate the MACs with `net0`/`net1` in the Proxmox VM hardware configuration. Linux names often look like `ens18`/`ens19`, but use the observed values rather than assuming them. Fill in these values from Organesson's validated policy and address allocations before continuing:

| Variable | Example shape |
| --- | --- |
| `WAN_IF`, `WAN_MAC` | Actual interface and MAC of `net0` |
| `WAN_CIDR` | The reserved WAN address with the environment prefix, for this lab `<allocated-address>/8` |
| `WAN_GATEWAY` | Current lab: `10.0.0.1` |
| `WAN_DNS_1`, `WAN_DNS_2` | Current lab: `10.0.0.2`, `10.0.0.3` |
| `LAN_IF`, `LAN_MAC` | Actual interface and MAC of `net1` |
| `LAN_CIDR` | The complete subnet assigned to this VNet, for example `192.168.250.0/24` only if that subnet was allocated |
| `LAN_GATEWAY` | Reserved router address inside `LAN_CIDR`, also configured as that VNet subnet's gateway |
| `DHCP_START`, `DHCP_END` | The non-overlapping dynamic range reserved for DHCP inside `LAN_CIDR` |
| `LAN_NETMASK` | Dotted mask matching the prefix; `/24` is `255.255.255.0` |
| `LAN_DOMAIN` | Deployment-local name such as `team-a.test` |

Create a static NetworkManager profile for each NIC, pinned to its MAC. The names intentionally use the same `organesson-<mac-without-colons>` convention as Organesson's current guest-network driver, avoiding competing profiles when the platform configures the NIC later. Run these commands in a root shell on the clone, after replacing every example value with the actual allocated values:

```sh
# Replace every value here with the actual values from this clone and Organesson.
WAN_IF='ens18'
WAN_MAC='BC:24:11:00:00:01'
WAN_CIDR='10.192.0.10/8'
WAN_GATEWAY='10.0.0.1'
WAN_DNS_1='10.0.0.2'
WAN_DNS_2='10.0.0.3'
LAN_IF='ens19'
LAN_MAC='BC:24:11:00:00:02'
LAN_CIDR='192.168.250.0/24'
LAN_GATEWAY='192.168.250.1'
DHCP_START='192.168.250.100'
DHCP_END='192.168.250.199'
LAN_NETMASK='255.255.255.0'
LAN_DOMAIN='team-a.test'

WAN_PROFILE="organesson-$(printf '%s' "$WAN_MAC" | tr -d ':' | tr '[:upper:]' '[:lower:]')"
LAN_PROFILE="organesson-$(printf '%s' "$LAN_MAC" | tr -d ':' | tr '[:upper:]' '[:lower:]')"

nmcli connection add type ethernet ifname "$WAN_IF" con-name "$WAN_PROFILE" \
  connection.autoconnect yes ethernet.mac-address "$WAN_MAC" \
  ipv4.method manual ipv4.addresses "$WAN_CIDR" ipv4.gateway "$WAN_GATEWAY" \
  ipv4.dns "$WAN_DNS_1,$WAN_DNS_2" ipv4.ignore-auto-dns yes ipv6.method disabled

nmcli connection add type ethernet ifname "$LAN_IF" con-name "$LAN_PROFILE" \
  connection.autoconnect yes ethernet.mac-address "$LAN_MAC" \
  ipv4.method manual ipv4.addresses "$LAN_GATEWAY/${LAN_CIDR##*/}" \
  ipv4.never-default yes ipv6.method disabled

nmcli connection up "$WAN_PROFILE"
nmcli connection up "$LAN_PROFILE"
```

The values above are examples only: `10.192.0.10` must be the address allocated to this router, and `192.168.250.0/24` must be the subnet actually assigned to the VNet. Do not paste the example values unchanged. Keep the values in the same root shell while creating the profiles; shell variables do not persist across separate logins.

This gives the router a default route only through the reserved WAN address; the LAN NIC has no gateway and cannot replace that default route. Disable IPv6 on both profiles for this first IPv4-only test. Add dual-stack only after IPv6 allocation, RA/DHCPv6, DNS, and firewall policy are designed.

### DHCP and DNS

Create `/etc/dnsmasq.d/organesson-router.conf` on the **clone**, replacing the variables with the values above:

```ini
interface=LAN_IF
except-interface=WAN_IF
listen-address=127.0.0.1,LAN_GATEWAY
bind-dynamic
local-service
no-resolv
domain-needed
bogus-priv
domain=LAN_DOMAIN
expand-hosts
local=/LAN_DOMAIN/
dhcp-authoritative
dhcp-range=DHCP_START,DHCP_END,LAN_NETMASK,12h
dhcp-option=option:router,LAN_GATEWAY
dhcp-option=option:dns-server,LAN_GATEWAY
```

Replace the uppercase tokens literally (for example, `interface=ens19`, `listen-address=127.0.0.1,192.168.250.1`, and `dhcp-range=192.168.250.100,192.168.250.199,255.255.255.0,12h`). Do not leave the placeholder names in the file. `dnsmasq` will serve DHCP only on the LAN and answer local names learned from DHCP leases.

For an egress-enabled deployment only, add these lines with the approved WAN DNS IPs:

```ini
server=WAN_DNS_1
server=WAN_DNS_2
```

With egress disabled, omit `server=` lines. In that mode DNS can answer local deployment names but must not forward arbitrary queries to the WAN.

### Firewall and optional egress

Create `/etc/nftables.conf` on the clone. This default denies new inbound and forwarded traffic. Replace the interface and subnet tokens as above:

```nft
#!/usr/sbin/nft -f
flush ruleset

table inet organesson {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        ct state invalid drop
        ct state established,related accept
        iifname "LAN_IF" ip protocol icmp accept
        iifname "LAN_IF" udp dport 67 accept
        iifname "LAN_IF" udp dport 53 accept
        iifname "LAN_IF" tcp dport 53 accept
    }

    chain forward {
        type filter hook forward priority filter; policy drop;
        ct state invalid drop
        ct state established,related accept
    }

    chain output {
        type filter hook output priority filter; policy drop;
        oifname "lo" accept
        ct state invalid drop
        ct state established,related accept
        oifname "LAN_IF" ip protocol icmp accept
        oifname "LAN_IF" udp sport 67 udp dport 68 accept
        oifname "LAN_IF" udp sport 53 accept
        oifname "LAN_IF" tcp sport 53 accept
        # Add the WAN DNS rules below only when egress/DNS forwarding is approved.
        oifname "WAN_IF" ip daddr { WAN_DNS_1, WAN_DNS_2 } udp dport 53 accept
        oifname "WAN_IF" ip daddr { WAN_DNS_1, WAN_DNS_2 } tcp dport 53 accept
    }
}
```

Before checking or enabling nftables, replace `LAN_IF`, `WAN_IF`, `LAN_CIDR`, `WAN_DNS_1`, and `WAN_DNS_2` in the rules with the actual interface names, subnet, and DNS addresses. The DNS address tokens appear only in the optional egress rules. For a fully isolated LAN, remove the two WAN DNS rules from `output` and leave the forward chain with its drop policy. Set `net.ipv4.ip_forward=0`.

For explicit Internet egress, add the following to `chain forward`, before the closing brace, and add the NAT table below. Keep the private-address drops before the general LAN-to-WAN accept rule so clients cannot reach lab/management addresses through the WAN:

```nft
        iifname "LAN_IF" oifname "WAN_IF" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } drop
        iifname "LAN_IF" oifname "WAN_IF" ip saddr LAN_CIDR accept
```

Then append:

```nft
table ip organesson_nat {
    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        oifname "WAN_IF" ip saddr LAN_CIDR masquerade
    }
}
```

In the egress-enabled case set `net.ipv4.ip_forward=1`. Do not add port-forwarding rules or WAN-side management in this first version. The private-range block is intentionally conservative for this lab; if a deployment must reach a specific private service, make that a separately reviewed policy exception rather than opening all RFC1918 destinations.

Set the forwarding sysctl according to the egress decision and validate before enabling services:

```sh
printf 'net.ipv4.ip_forward=%s\n' '0-or-1' | sudo tee /etc/sysctl.d/90-organesson-router.conf
sudo sysctl --system
sudo dnsmasq --test
sudo nft --check --file /etc/nftables.conf
sudo systemctl enable --now nftables.service dnsmasq.service
sudo systemctl --no-pager --full status nftables.service dnsmasq.service
sudo nft list ruleset
```

Replace `0-or-1` with `0` for isolated LAN or `1` for explicitly approved routed egress. `nftables` provides stateful filtering and NAT/masquerading; review its [manual](https://netfilter.org/projects/nftables/manpage.html) before adding rules. Do not enable the services until both syntax checks pass.

### Clone acceptance checks

Attach one test client to the LAN VNet and set its NIC to DHCP. Verify:

1. It receives an address within the approved DHCP range, with the router's LAN address as gateway and DNS.
2. It can ping the router's LAN address and another client on the VNet.
3. A local DHCP client name resolves through the router's LAN DNS address.
4. If egress is disabled, a client cannot reach outside the LAN. If egress is enabled, an Internet lookup and connection work while destinations in `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, and `169.254.0.0/16` remain blocked.
5. From the WAN side, new connections to the router's DHCP, DNS, SSH, and management ports fail. No SSH server or web admin service should be installed.
6. A reboot preserves the addresses, leases, DNS behavior, and firewall rules. The router's LAN MAC/IP is unique to this clone.
7. If cross-node operation is needed, migrate the router or test client to another Proxmox node and repeat the LAN checks.

Only after all checks pass should this source alias be marked ready for router cloning. Keep the test router and client under pool `organesson` and disks on `laas`; remove only those test clones when finished. Never remove the source VM or the imported `ogvxlan` zone as part of a clone test.

## Updating and distributing the source

For an update, keep the source powered off until maintenance. Temporarily attach an approved maintenance NIC, boot the source, update packages, and rerun `og-prep-linux.sh` last. Shut down without rebooting, remove that NIC, and validate a disposable full clone again. Never clone while the source is running or while its identity has been regenerated by a reboot after prep.

Keep this build procedure, configuration examples, source ISO name/version, package manifest, and a checksum record in the repository. Do not commit a raw VM disk image: it is large, expensive for every clone to download, and easy to distribute with a maintenance account or machine identity still present. If a prebuilt image is useful later, publish it as a separately versioned release/artifact with a checksum and rebuild instructions after a clean-clone security review.
