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

qemu_ga_path=""
for candidate in /usr/local/bin/qemu-ga /usr/local/sbin/qemu-ga; do
    if [ -x "$candidate" ]; then
        qemu_ga_path="$candidate"
        break
    fi
done
if [ -z "$qemu_ga_path" ]; then
    log "Installing the QEMU Guest Agent package."
    pkg update -f
    pkg install -y qemu-guest-agent
    for candidate in /usr/local/bin/qemu-ga /usr/local/sbin/qemu-ga; do
        if [ -x "$candidate" ]; then
            qemu_ga_path="$candidate"
            break
        fi
    done
fi
[ -n "$qemu_ga_path" ] || fail "qemu-guest-agent is installed, but qemu-ga was not found in /usr/local/bin or /usr/local/sbin."
log "Found QEMU Guest Agent at $qemu_ga_path."

if [ ! -e /dev/vtcon/org.qemu.guest_agent.0 ]; then
    log "Loading the VirtIO console driver required for the QEMU Guest Agent channel."
    if ! kldload virtio_console && [ ! -e /dev/vtcon/org.qemu.guest_agent.0 ]; then
        fail "Could not load virtio_console or find the QEMU Guest Agent channel."
    fi
fi
sysrc -f /boot/loader.conf virtio_console_load="YES" >/dev/null
[ -e /dev/vtcon/org.qemu.guest_agent.0 ] || fail "The VirtIO QEMU Guest Agent channel /dev/vtcon/org.qemu.guest_agent.0 is unavailable."

sysrc qemu_guest_agent_enable="YES"
if ! service qemu-guest-agent status >/dev/null 2>&1; then
    service qemu-guest-agent onestart
fi
service qemu-guest-agent status >/dev/null 2>&1 || fail "QEMU Guest Agent did not start."

log "Removing cloned FreeBSD host identity."
rm -f /etc/hostid
for key in /etc/ssh/ssh_host_*; do
    [ -e "$key" ] || continue
    rm -f -- "$key"
done

log "Preparation complete for $(uname -sr)."
log "Remove this temporary script, then shut down the source after validation."
