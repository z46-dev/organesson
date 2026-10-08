#!/bin/sh

set -eu

log() {
    printf '[organesson-template-prep] %s\n' "$*"
}

fail() {
    log "ERROR: $*" >&2
    exit 1
}

[ "$(id -u)" -eq 0 ] || fail "Run this script as root."
[ "$(uname -s)" = "FreeBSD" ] || fail "This preparation script supports FreeBSD only."
command -v pkg >/dev/null 2>&1 || fail "FreeBSD pkg is required."
command -v sysrc >/dev/null 2>&1 || fail "FreeBSD sysrc is required."

log "Updating FreeBSD packages."
pkg update -f
pkg upgrade -y
pkg install -y qemu-guest-agent

[ -x /usr/local/sbin/qemu-ga ] || fail "qemu-guest-agent did not install /usr/local/sbin/qemu-ga."
sysrc qemu_guest_agent_enable="YES"
service qemu-guest-agent onestart
service qemu-guest-agent status >/dev/null 2>&1 || fail "QEMU Guest Agent did not start."

log "Removing cloned FreeBSD host identity."
rm -f /etc/hostid
for key in /etc/ssh/ssh_host_*; do
    [ -e "$key" ] || continue
    rm -f -- "$key"
done

log "Preparation complete for $(uname -sr)."
log "Remove this temporary script, then shut down the source after validation."
