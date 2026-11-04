#!/bin/bash
set -euo pipefail

config_file="$PWD/organesson-router.conf"
if [[ ! -f "$config_file" ]]; then
    echo "Organesson router configuration is missing" >&2
    exit 1
fi
source "$config_file"

find_interface() {
    local expected_mac="${1,,}"
    local device
    for device in /sys/class/net/*; do
        [[ -e "$device/address" ]] || continue
        if [[ "$(<"$device/address")" == "$expected_mac" ]]; then
            basename "$device"
            return 0
        fi
    done
    echo "No interface found for configured MAC $expected_mac" >&2
    return 1
}

lan_interface="$(find_interface "$LAN_MAC")"
wan_interface=""
external_vlan_interface=""
if [[ -n "$WAN_MAC" ]]; then
    wan_interface="$(find_interface "$WAN_MAC")"
fi
if [[ -n "${EXTERNAL_VLAN_MAC:-}" ]]; then
    external_vlan_interface="$(find_interface "$EXTERNAL_VLAN_MAC")"
fi

if [[ -n "$external_vlan_interface" ]]; then
    lan_port="$lan_interface"
    vlan_port="$external_vlan_interface"
    bridge_device="ogbr0"
    install -d -m 0755 /usr/local/sbin
    cat >/usr/local/sbin/organesson-lan-bridge <<EOF
#!/bin/bash
set -euo pipefail
ip link add name $bridge_device type bridge 2>/dev/null || true
ip link set "$bridge_device" address "$LAN_MAC"
ip link set "$lan_port" down
ip link set "$vlan_port" down
ip addr flush dev "$lan_port"
ip addr flush dev "$vlan_port"
ip link set "$lan_port" master $bridge_device
ip link set "$vlan_port" master $bridge_device
ip link set "$lan_port" up
ip link set "$vlan_port" up
ip link set "$bridge_device" up
EOF
    if [[ -n "$LAN_ADDRESS" ]]; then
        printf 'ip address replace %s/%s dev %s\n' "$LAN_ADDRESS" "$LAN_PREFIX" "$bridge_device" >>/usr/local/sbin/organesson-lan-bridge
    fi
    if [[ -n "${LAN_IPV6_ADDRESS:-}" ]]; then
        printf 'ip -6 address replace %s/%s dev %s\n' "$LAN_IPV6_ADDRESS" "$LAN_IPV6_PREFIX" "$bridge_device" >>/usr/local/sbin/organesson-lan-bridge
    fi
    chmod 0755 /usr/local/sbin/organesson-lan-bridge
    cat >/etc/systemd/system/organesson-lan-bridge.service <<EOF
[Unit]
Description=Organesson router LAN external VLAN bridge
After=NetworkManager.service
Wants=NetworkManager.service
Before=dnsmasq.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/organesson-lan-bridge
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
    /usr/local/sbin/organesson-lan-bridge
    systemctl enable organesson-lan-bridge.service
    lan_interface="$bridge_device"
fi

if ! command -v dnsmasq >/dev/null 2>&1 || ! command -v nft >/dev/null 2>&1; then
    echo "Router template must include dnsmasq and nftables" >&2
    exit 1
fi

install -d -m 0755 /etc/dnsmasq.d
install -d -m 0755 /etc/organesson
touch /etc/organesson/dnsmasq-reservations
listen_addresses="127.0.0.1"
if [[ -n "$LAN_ADDRESS" ]]; then
    listen_addresses+=",$LAN_ADDRESS"
fi
if [[ -n "${LAN_IPV6_ADDRESS:-}" ]]; then
    listen_addresses+=",$LAN_IPV6_ADDRESS"
fi
cat >/etc/dnsmasq.d/organesson-router.conf <<EOF
interface=$lan_interface
bind-dynamic
listen-address=$listen_addresses
dhcp-hostsfile=/etc/organesson/dnsmasq-reservations
domain-needed
bogus-priv
EOF
if [[ -n "$LAN_ADDRESS" && -n "$DHCP_START" ]]; then
    printf 'dhcp-range=%s,%s,12h\n' "$DHCP_START" "$DHCP_END" >>/etc/dnsmasq.d/organesson-router.conf
    printf 'dhcp-option=option:router,%s\n' "$LAN_ADDRESS" >>/etc/dnsmasq.d/organesson-router.conf
    if [[ ${#DNS_SERVERS[@]} -gt 0 ]]; then
        printf 'dhcp-option=option:dns-server,%s\n' "$(IFS=,; echo "${DNS_SERVERS[*]}")" >>/etc/dnsmasq.d/organesson-router.conf
    fi
fi
if [[ -n "${LAN_IPV6_ADDRESS:-}" ]]; then
    printf 'enable-ra\n' >>/etc/dnsmasq.d/organesson-router.conf
    if [[ -n "${DHCPV6_START:-}" ]]; then
        printf 'dhcp-range=%s,%s,12h\n' "$DHCPV6_START" "$DHCPV6_END" >>/etc/dnsmasq.d/organesson-router.conf
    else
        printf 'dhcp-range=::,constructor:%s,ra-stateless,ra-names,12h\n' "$lan_interface" >>/etc/dnsmasq.d/organesson-router.conf
    fi
    if [[ ${#DNSV6_SERVERS[@]} -gt 0 ]]; then
        ipv6_dns_option=""
        for dns_server in "${DNSV6_SERVERS[@]}"; do
            ipv6_dns_option+="[$dns_server],"
        done
        ipv6_dns_option=${ipv6_dns_option%,}
        printf 'dhcp-option=option6:dns-server,%s\n' "$ipv6_dns_option" >>/etc/dnsmasq.d/organesson-router.conf
    fi
fi

install -d -m 0755 /etc/sysctl.d
if [[ -n "$wan_interface" ]]; then
    printf 'net.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=1\nnet.ipv6.conf.default.forwarding=1\n' >/etc/sysctl.d/90-organesson-router.conf
    cat >/etc/nftables.conf <<EOF
#!/usr/sbin/nft -f
flush ruleset
table inet organesson {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        ct state established,related accept
        iifname "$wan_interface" meta l4proto ipv6-icmp icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-neighbor-solicit, nd-neighbor-advert } accept
        iifname "$lan_interface" udp dport { 53, 67 } accept
        iifname "$lan_interface" udp dport { 546, 547 } accept
        iifname "$lan_interface" tcp dport 53 accept
        iifname "$lan_interface" ip protocol icmp accept
        iifname "$lan_interface" meta l4proto ipv6-icmp accept
    }
    chain forward {
        type filter hook forward priority filter; policy drop;
        ct state established,related accept
        iifname "$lan_interface" oifname "$wan_interface" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } drop
        iifname "$lan_interface" oifname "$wan_interface" meta nfproto ipv6 ip6 daddr { fc00::/7, fe80::/10 } drop
        iifname "$lan_interface" oifname "$wan_interface" accept
    }
}
EOF
    if [[ -n "$LAN_SUBNET" ]]; then
        cat >>/etc/nftables.conf <<EOF
table ip organesson_nat {
    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        oifname "$wan_interface" ip saddr $LAN_SUBNET masquerade
    }
}
EOF
    fi
    if [[ -n "${LAN_IPV6_SUBNET:-}" ]]; then
        cat >>/etc/nftables.conf <<EOF
table ip6 organesson_nat {
    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        oifname "$wan_interface" ip6 saddr $LAN_IPV6_SUBNET masquerade
    }
}
EOF
    fi
else
    printf 'net.ipv4.ip_forward=0\nnet.ipv6.conf.all.forwarding=0\nnet.ipv6.conf.default.forwarding=0\n' >/etc/sysctl.d/90-organesson-router.conf
    cat >/etc/nftables.conf <<EOF
#!/usr/sbin/nft -f
flush ruleset
table inet organesson {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        ct state established,related accept
        iifname "$lan_interface" udp dport { 53, 67 } accept
        iifname "$lan_interface" udp dport { 546, 547 } accept
        iifname "$lan_interface" tcp dport 53 accept
        iifname "$lan_interface" ip protocol icmp accept
        iifname "$lan_interface" meta l4proto ipv6-icmp accept
    }
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
}
EOF
fi

sysctl --system >/dev/null
dnsmasq --test
nft --check --file /etc/nftables.conf
systemctl enable nftables.service dnsmasq.service
systemctl restart nftables.service dnsmasq.service
rm -f "$config_file"
