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
if [[ -n "$WAN_MAC" ]]; then
    wan_interface="$(find_interface "$WAN_MAC")"
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
    printf 'net.ipv4.ip_forward=1\n' >/etc/sysctl.d/90-organesson-router.conf
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
        ct state established,related accept
        iifname "$lan_interface" oifname "$wan_interface" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } drop
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
else
    printf 'net.ipv4.ip_forward=0\n' >/etc/sysctl.d/90-organesson-router.conf
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
