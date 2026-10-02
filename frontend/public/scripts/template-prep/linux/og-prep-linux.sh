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
    debian:12|debian:13)
        package_manager="apt-get"
        ;;
    rhel:8.*|rhel:9.*|rhel:10.*|rocky:8.*|rocky:9.*|rocky:10.*|almalinux:8.*|almalinux:9.*|almalinux:10.*)
        package_manager="dnf"
        ;;
    rhel:8|rhel:9|rhel:10|rocky:8|rocky:9|rocky:10|almalinux:8|almalinux:9|almalinux:10)
        package_manager="dnf"
        ;;
    *)
        fail "Unsupported OS: ${PRETTY_NAME:-${os_id} ${os_version}}. Supported: Fedora 43-45, Ubuntu 24.04/26.04 LTS, Debian 12/13, RHEL/Rocky/AlmaLinux 8-10."
        ;;
esac

require_command systemctl
require_command awk
require_command cat
require_command find
require_command grep
require_command install
require_command rm
require_command truncate
require_command "${package_manager}"

configure_selinux_qemu_guest_agent() {
    local selinux_mode=""
    local qga_context=""
    local qga_wrapper="/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec"
    local expected_context=""
    local boolean_state=""
    local hook_directory=""
    local existing_hook=""

    if ! command -v getenforce >/dev/null 2>&1; then
        if [[ -e /sys/fs/selinux/enforce ]]; then
            fail "SELinux is active but its administration tools are missing. Install policycoreutils, then rerun this script."
        fi
        log "SELinux is not active; no QEMU Guest Agent SELinux setup is needed."
        return
    fi

    selinux_mode="$(getenforce)" || fail "Could not determine the SELinux mode."
    if [[ "${selinux_mode}" == "Disabled" ]]; then
        log "SELinux is disabled; no QEMU Guest Agent SELinux setup is needed."
        return
    fi
    if [[ "${selinux_mode}" != "Enforcing" && "${selinux_mode}" != "Permissive" ]]; then
        fail "Unexpected SELinux mode: ${selinux_mode}."
    fi

    qga_context="$(cat "/proc/${agent_pid}/attr/current")" || fail "Could not inspect the QEMU Guest Agent SELinux context."
    case "${qga_context}" in
        *:virt_qemu_ga_t:*)
            ;;
        *:virt_qemu_ga_unconfined_t:*|*:unconfined_t:*|unconfined)
            log "QEMU Guest Agent is already outside the confined SELinux domain; no SELinux transition setup is needed."
            return
            ;;
        *)
            fail "SELinux is ${selinux_mode}, but QEMU Guest Agent has unexpected context '${qga_context}'. Review its policy before using this source."
            ;;
    esac

    require_command getsebool
    require_command setsebool
    require_command matchpathcon
    require_command restorecon
    if ! boolean_state="$(getsebool virt_qemu_ga_run_unconfined 2>/dev/null)"; then
        fail "SELinux confines QEMU Guest Agent but the virt_qemu_ga_run_unconfined boolean is unavailable. Update the SELinux policy or define another privileged execution method."
    fi

    for hook_directory in /etc/qemu-ga/fsfreeze-hook.d /usr/libexec/qemu-ga/fsfreeze-hook.d /var/run/qemu-ga/fsfreeze-hook.d; do
        if [[ -d "${hook_directory}" ]]; then
            while IFS= read -r -d '' existing_hook; do
                [[ "${existing_hook}" == "${qga_wrapper}" ]] || fail "Found an existing executable QEMU Guest Agent hook at ${existing_hook}; review it before enabling unconfined hook execution."
            done < <(find "${hook_directory}" -mindepth 1 -maxdepth 1 -type f -executable -print0)
        fi
    done

    if [[ -e "${qga_wrapper}" ]]; then
        [[ -f "${qga_wrapper}" && ! -L "${qga_wrapper}" ]] || fail "Refusing to replace a non-regular QEMU Guest Agent wrapper at ${qga_wrapper}."
        grep -Fqx '# Managed by og-prep-linux.sh.' "${qga_wrapper}" || fail "Refusing to replace an unmanaged file at ${qga_wrapper}."
    fi

    install -d -o root -g root -m 0755 "${qga_wrapper%/*}"
    cat > "${qga_wrapper}" <<'EOF'
#!/bin/sh
# Managed by og-prep-linux.sh.
set -eu

if [ "$#" -eq 0 ]; then
    echo "usage: organesson-qga-exec <command> [arguments...]" >&2
    exit 2
fi

exec "$@"
EOF
    chown root:root "${qga_wrapper}"
    chmod 0755 "${qga_wrapper}"
    restorecon -v "${qga_wrapper}"
    expected_context="$(matchpathcon -n "${qga_wrapper}")" || fail "Could not verify the QEMU Guest Agent wrapper SELinux label."
    [[ "${expected_context}" == *:virt_qemu_ga_unconfined_exec_t:* ]] || fail "SELinux assigned an unexpected wrapper label: ${expected_context}."

    setsebool -P virt_qemu_ga_run_unconfined on
    boolean_state="$(getsebool virt_qemu_ga_run_unconfined)" || fail "Could not verify the QEMU Guest Agent SELinux boolean."
    [[ "${boolean_state}" == "virt_qemu_ga_run_unconfined --> on" ]] || fail "QEMU Guest Agent SELinux boolean did not enable: ${boolean_state}."
    log "Enabled the SELinux QEMU Guest Agent transition and installed ${qga_wrapper}. Guest commands requiring full root access must use this wrapper."
    log "WARNING: the wrapper allows QEMU Guest Agent callers to run arbitrary root commands; restrict guest-agent access to trusted management-plane users."
}

verify_apparmor_qemu_guest_agent() {
    local apparmor_context_path="/proc/${agent_pid}/attr/apparmor/current"
    local apparmor_enabled_path="/sys/module/apparmor/parameters/enabled"
    local apparmor_enabled=""
    local apparmor_context=""

    if [[ ! -r "${apparmor_enabled_path}" ]]; then
        return
    fi
    apparmor_enabled="$(cat "${apparmor_enabled_path}")" || fail "Could not determine whether AppArmor is enabled."
    if [[ "${apparmor_enabled}" != "Y" ]]; then
        return
    fi
    [[ -r "${apparmor_context_path}" ]] || fail "AppArmor is enabled but the QEMU Guest Agent profile cannot be inspected."

    apparmor_context="$(cat "${apparmor_context_path}")" || fail "Could not inspect the QEMU Guest Agent AppArmor context."
    if [[ "${apparmor_context}" != "unconfined" ]]; then
        fail "QEMU Guest Agent is confined by AppArmor profile '${apparmor_context}'. This script does not weaken or replace that profile; validate a dedicated privileged execution method before registering the source."
    fi
    log "QEMU Guest Agent is unconfined by AppArmor; no AppArmor policy changes are needed."
}

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
configure_selinux_qemu_guest_agent
verify_apparmor_qemu_guest_agent

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
