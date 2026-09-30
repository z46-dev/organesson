#!/usr/bin/env bash

set -Eeuo pipefail

log() {
    printf '[organesson-template-prep] %s\n' "$*"
}

fail() {
    log "ERROR: $*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "Required command not found: $1"
}

if [[ "${EUID}" -ne 0 ]]; then
    fail "Run this script as root (for example: sudo bash og-prep-linux.sh)."
fi

if [[ ! -r /etc/os-release ]]; then
    fail "Cannot identify this OS: /etc/os-release is missing or unreadable."
fi

# shellcheck disable=SC1091
source /etc/os-release

os_id="${ID:-unknown}"
os_version="${VERSION_ID:-unknown}"
os_major="${os_version%%.*}"
package_manager=""

case "${os_id}:${os_version}" in
    fedora:43|fedora:44|fedora:45)
        package_manager="dnf"
        ;;
    ubuntu:24.04|ubuntu:26.04)
        package_manager="apt-get"
        ;;
    rhel:8.*|rhel:9.*|rhel:10.*|rocky:8.*|rocky:9.*|rocky:10.*|almalinux:8.*|almalinux:9.*|almalinux:10.*)
        package_manager="dnf"
        ;;
    rhel:8|rhel:9|rhel:10|rocky:8|rocky:9|rocky:10|almalinux:8|almalinux:9|almalinux:10)
        package_manager="dnf"
        ;;
    *)
        fail "Unsupported OS: ${PRETTY_NAME:-${os_id} ${os_version}}. Supported: Fedora 43-45, Ubuntu 24.04/26.04 LTS, RHEL/Rocky/AlmaLinux 8-10."
        ;;
esac

require_command systemctl
require_command awk
require_command rm
require_command truncate
require_command "${package_manager}"

if [[ "${package_manager}" == "dnf" ]]; then
    log "Updating ${PRETTY_NAME:-${os_id} ${os_version}}."
    dnf --refresh --assumeyes upgrade
    dnf --assumeyes install qemu-guest-agent
    dnf clean all
else
    log "Updating ${PRETTY_NAME:-${os_id} ${os_version}}."
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get --assume-yes full-upgrade
    apt-get --assume-yes install qemu-guest-agent
    apt-get clean
fi

log "Enabling and starting QEMU Guest Agent."
systemctl enable --now qemu-guest-agent.service
systemctl is-active --quiet qemu-guest-agent.service || fail "QEMU Guest Agent service is not active."

agent_pid="$(systemctl show --property=MainPID --value qemu-guest-agent.service)"
[[ "${agent_pid}" =~ ^[1-9][0-9]*$ ]] || fail "QEMU Guest Agent has no running process."
agent_uid="$(awk '/^Uid:/ { print $3 }' "/proc/${agent_pid}/status")"
[[ "${agent_uid}" == "0" ]] || fail "QEMU Guest Agent is not running as root (effective UID: ${agent_uid:-unknown})."

log "Removing cloned machine identity."
truncate --size 0 /etc/machine-id
if [[ -e /var/lib/dbus/machine-id && ! -L /var/lib/dbus/machine-id ]]; then
    rm --force /var/lib/dbus/machine-id
fi
rm --force /etc/ssh/ssh_host_*
rm --force /var/lib/systemd/random-seed

[[ ! -s /etc/machine-id ]] || fail "Could not clear /etc/machine-id."

log "Preparation complete for ${PRETTY_NAME:-${os_id} ${os_version}}."
log "Delete this script, then shut down this source VM without rebooting it before cloning."
