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
package_manager=""
network_backend="NetworkManager"

wicked_owns_network_service() {
    [[ -L /etc/systemd/system/network.service ]] || return 1
    [[ "$(readlink -f /etc/systemd/system/network.service)" == */wicked.service ]]
}

networkd_owns_ethernet() {
    command -v systemctl >/dev/null 2>&1 || return 1
    systemctl is-active --quiet systemd-networkd.service || return 1
    command -v networkctl >/dev/null 2>&1 || return 1
    networkctl --no-legend --no-pager list 2>/dev/null | awk '$3 == "ether" && $5 != "unmanaged" { found = 1 } END { exit !found }'
}

case "${os_id}" in
    debian|ubuntu|linuxmint|pop)
        package_manager="apt-get"
        ;;
    fedora|rhel|rocky|almalinux|centos|ol|oracle|amzn)
        if command -v dnf >/dev/null 2>&1; then
            package_manager="dnf"
        elif command -v yum >/dev/null 2>&1; then
            package_manager="yum"
        else
            fail "No supported dnf/yum package manager was found."
        fi
        ;;
    opensuse*|sles|sled)
        package_manager="zypper"
        # openSUSE and SLES commonly use wicked, which owns the network.service
        # alias. Preserve that choice instead of trying to enable NetworkManager
        # over an existing alias.
        if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet NetworkManager.service; then
            network_backend="NetworkManager"
        elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet wicked.service; then
            network_backend="wicked"
        elif [[ -L /etc/systemd/system/network.service ]] && [[ "$(readlink -f /etc/systemd/system/network.service)" == */wicked.service ]]; then
            network_backend="wicked"
        elif command -v wicked >/dev/null 2>&1; then
            network_backend="wicked"
        fi
        ;;
    alpine)
        package_manager="apk"
        ;;
    arch|manjaro|endeavouros)
        package_manager="pacman"
        ;;
    *)
        fail "Unsupported Linux distribution '${os_id}' (${PRETTY_NAME:-${os_version}}). Supported package families: apt, dnf/yum, zypper, apk, and pacman."
        ;;
esac

# The service alias is authoritative even if the distro ID or active-state
# probes are unusual. Never enable NetworkManager over wicked's alias.
if wicked_owns_network_service; then
    network_backend="wicked"
elif networkd_owns_ethernet; then
    network_backend="systemd-networkd"
elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet NetworkManager.service; then
    network_backend="NetworkManager"
elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet wicked.service; then
    network_backend="wicked"
elif command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd.service; then
    network_backend="systemd-networkd"
fi

for command_name in awk cat chown chmod cmp find grep install mktemp mv readlink rm truncate; do
    require_command "${command_name}"
done
require_command "${package_manager}"

install_packages() {
    case "${package_manager}" in
        apt-get)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update
            apt-get --assume-yes full-upgrade
            if [[ "${network_backend}" == "NetworkManager" ]]; then
                apt-get --assume-yes install network-manager qemu-guest-agent
            else
                apt-get --assume-yes install qemu-guest-agent
            fi
            apt-get clean
            ;;
        dnf)
            dnf --refresh --assumeyes upgrade
            if [[ "${network_backend}" == "NetworkManager" ]]; then
                dnf --assumeyes install NetworkManager qemu-guest-agent policycoreutils
            else
                dnf --assumeyes install qemu-guest-agent policycoreutils
            fi
            dnf clean all
            ;;
        yum)
            yum --assumeyes update
            if [[ "${network_backend}" == "NetworkManager" ]]; then
                yum --assumeyes install NetworkManager qemu-guest-agent policycoreutils
            else
                yum --assumeyes install qemu-guest-agent policycoreutils
            fi
            yum clean all
            ;;
        zypper)
            zypper --non-interactive refresh
            zypper --non-interactive update
            if [[ "${network_backend}" == "wicked" ]]; then
                zypper --non-interactive install wicked qemu-guest-agent
            elif [[ "${network_backend}" == "systemd-networkd" ]]; then
                zypper --non-interactive install qemu-guest-agent
            else
                zypper --non-interactive install NetworkManager qemu-guest-agent
            fi
            zypper clean --all
            ;;
        apk)
            apk update
            apk upgrade
            if [[ "${network_backend}" == "NetworkManager" ]]; then
                apk add networkmanager qemu-guest-agent qemu-guest-agent-openrc
            else
                apk add qemu-guest-agent qemu-guest-agent-openrc
            fi
            ;;
        pacman)
            if [[ "${network_backend}" == "NetworkManager" ]]; then
                pacman --noconfirm --needed -Syu networkmanager qemu-guest-agent
            else
                pacman --noconfirm --needed -Syu qemu-guest-agent
            fi
            ;;
    esac
}

configure_rhel_qemu_guest_agent_rpcs() {
    local config_path="/etc/sysconfig/qemu-ga"
    local temporary_path=""
    local required_rpcs="guest-exec,guest-exec-status,guest-file-open,guest-file-write,guest-file-close"

    case "${os_id}" in
        fedora|rhel|rocky|almalinux|centos|ol|oracle|amzn)
            ;;
        *)
            return
            ;;
    esac

    if [[ ! -e "${config_path}" ]]; then
        log "No RHEL-family QEMU Guest Agent RPC filter found; leaving its default policy unchanged."
        return
    fi
    [[ -f "${config_path}" && ! -L "${config_path}" ]] || fail "Refusing to modify a non-regular QEMU Guest Agent configuration at ${config_path}."

    temporary_path="$(mktemp "${config_path}.organesson.XXXXXX")" || fail "Could not prepare a temporary QEMU Guest Agent configuration."
    if ! awk -v required="${required_rpcs}" '
        function contains(list, item, items, count, item_index) {
            count = split(list, items, ",")
            for (item_index = 1; item_index <= count; item_index++) {
                if (items[item_index] == item) {
                    return 1
                }
            }
            return 0
        }
        function add_required(list, required_list, items, count, item_index, output) {
            output = list
            count = split(required_list, items, ",")
            for (item_index = 1; item_index <= count; item_index++) {
                if (!contains(output, items[item_index])) {
                    output = output (output == "" ? "" : ",") items[item_index]
                }
            }
            return output
        }
        function remove_required(list, required_list, items, count, item_index, current, output) {
            output = ""
            count = split(list, items, ",")
            for (item_index = 1; item_index <= count; item_index++) {
                current = items[item_index]
                if (current != "" && !contains(required_list, current)) {
                    output = output (output == "" ? "" : ",") current
                }
            }
            return output
        }
        {
            line = $0
            active = line
            sub(/^[[:space:]]*/, "", active)
            if (active ~ /^FILTER_RPC_ARGS=/ && active !~ /^#/) {
                if (match(line, /--allow-rpcs=[^"[:space:]]+/)) {
                    option = substr(line, RSTART, RLENGTH)
                    list = substr(option, length("--allow-rpcs=") + 1)
                    updated = "--allow-rpcs=" add_required(list, required)
                    line = substr(line, 1, RSTART - 1) updated substr(line, RSTART + RLENGTH)
                } else if (match(line, /--block-rpcs=[^"[:space:]]*/)) {
                    option = substr(line, RSTART, RLENGTH)
                    list = substr(option, length("--block-rpcs=") + 1)
                    updated = "--block-rpcs=" remove_required(list, required)
                    line = substr(line, 1, RSTART - 1) updated substr(line, RSTART + RLENGTH)
                }
            } else if (active ~ /^BLACKLIST_RPC=/ && active !~ /^#/) {
                value = active
                sub(/^BLACKLIST_RPC=/, "", value)
                quote = ""
                if (substr(value, 1, 1) == "\"" || substr(value, 1, 1) == "\047") {
                    quote = substr(value, 1, 1)
                    value = substr(value, 2)
                    if (substr(value, length(value), 1) == quote) {
                        value = substr(value, 1, length(value) - 1)
                    }
                }
                updated = remove_required(value, required)
                if (quote != "") {
                    updated = quote updated quote
                }
                sub(/BLACKLIST_RPC=.*/, "BLACKLIST_RPC=" updated, line)
            }
            print line
        }
    ' "${config_path}" > "${temporary_path}"; then
        rm -f -- "${temporary_path}"
        fail "Could not inspect QEMU Guest Agent RPC restrictions in ${config_path}."
    fi

    if cmp -s "${config_path}" "${temporary_path}"; then
        rm -f -- "${temporary_path}"
        log "QEMU Guest Agent RPC policy already allows Organesson's required execution and script-transfer commands."
        return
    fi

    chmod --reference="${config_path}" "${temporary_path}"
    chown --reference="${config_path}" "${temporary_path}"
    mv -- "${temporary_path}" "${config_path}"
    log "Updated ${config_path} to allow Organesson's guest-exec and script-transfer RPCs while preserving the other RPC policy."
    log "Restart qemu-guest-agent after running this script locally in the guest; do not restart it from a guest-agent-launched script."
}

enable_service() {
    local service_name="$1"
    if [[ "${service_name}" == "NetworkManager" ]] && wicked_owns_network_service; then
        fail "wicked owns network.service; refusing to enable NetworkManager over the existing wicked alias."
    fi
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now "${service_name}.service"
        systemctl is-active --quiet "${service_name}.service" || fail "${service_name}.service is not active."
    elif command -v rc-update >/dev/null 2>&1 && command -v rc-service >/dev/null 2>&1; then
        rc-update add "${service_name}" default
        rc-service "${service_name}" start
        rc-service "${service_name}" status >/dev/null || fail "${service_name} service is not active."
    else
        fail "Neither systemd nor OpenRC service management is available."
    fi
}

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

ensure_usable_ethernet_connection() {
    local device=""
    local device_type=""
    local device_state=""
    local candidate=""
    local attempt=0
    local -a connected_devices=()
    local -a available_devices=()

    if wicked_owns_network_service; then
        network_backend="wicked"
        ensure_usable_wicked_connection
        return
    fi
    if networkd_owns_ethernet; then
        network_backend="systemd-networkd"
        ensure_usable_networkd_connection
        return
    fi

    require_command nmcli
    require_command ip
    enable_service NetworkManager
    [[ "$(nmcli -t -f RUNNING general)" == "running" ]] || fail "NetworkManager is not ready to manage the source NIC."

    while IFS=: read -r device device_type device_state; do
        [[ "${device_type}" == "ethernet" ]] || continue
        case "${device_state}" in
            connected)
                connected_devices+=("${device}")
                ;;
            unavailable|unmanaged)
                ;;
            *)
                available_devices+=("${device}")
                ;;
        esac
    done < <(nmcli --terse --fields DEVICE,TYPE,STATE device status)

    for candidate in "${connected_devices[@]}" "${available_devices[@]}"; do
        [[ -n "${candidate}" ]] || continue
        if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
            log "Using active source NIC ${candidate}."
            return
        fi

        log "Trying to activate source NIC ${candidate}."
        if ! nmcli device connect "${candidate}"; then
            log "Could not activate ${candidate}; checking the next Ethernet device."
            continue
        fi
        for ((attempt = 0; attempt < 20; attempt++)); do
            if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
                log "Activated source NIC ${candidate}."
                return
            fi
            sleep 1
        done
        log "${candidate} did not receive a global IP address; checking the next Ethernet device."
    done

    fail "No usable Ethernet device is connected. Reconnect a source NIC and rerun preparation; NetworkManager fallback behavior was left unchanged."
}

ensure_usable_wicked_connection() {
    local address_file=""
    local candidate=""
    local attempt=0

    require_command wicked
    require_command ip
    if command -v systemctl >/dev/null 2>&1; then
        systemctl start wicked.service || fail "Could not start wicked.service."
    fi

    for address_file in /sys/class/net/*/address; do
        [[ -r "${address_file}" ]] || continue
        candidate="${address_file%/address}"
        candidate="${candidate##*/}"
        [[ "${candidate}" == "lo" ]] && continue

        if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
            log "Using active source NIC ${candidate} managed by wicked."
            return
        fi

        log "Trying to activate source NIC ${candidate} through wicked."
        if ! wicked ifup "${candidate}"; then
            log "Could not activate ${candidate}; checking the next Ethernet device."
            continue
        fi
        for ((attempt = 0; attempt < 20; attempt++)); do
            if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
                log "Activated source NIC ${candidate} through wicked."
                return
            fi
            sleep 1
        done
        log "${candidate} did not receive a global IP address; checking the next Ethernet device."
    done

    fail "No usable Ethernet device is connected. Reconnect a source NIC and rerun preparation; existing wicked interface configuration was left unchanged."
}

ensure_usable_networkd_connection() {
    local address_file=""
    local candidate=""
    local attempt=0

    require_command networkctl
    require_command ip

    for address_file in /sys/class/net/*/address; do
        [[ -r "${address_file}" ]] || continue
        candidate="${address_file%/address}"
        candidate="${candidate##*/}"
        [[ "${candidate}" == "lo" ]] && continue

        if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
            log "Using active source NIC ${candidate} managed by systemd-networkd."
            return
        fi

        log "Trying to reapply the existing systemd-networkd configuration to ${candidate}."
        if ! networkctl reconfigure "${candidate}"; then
            log "Could not reconfigure ${candidate}; checking the next Ethernet device."
            continue
        fi
        for ((attempt = 0; attempt < 20; attempt++)); do
            if ip -o address show dev "${candidate}" scope global | grep -qE ' inet(6)? '; then
                log "Activated source NIC ${candidate} through systemd-networkd."
                return
            fi
            sleep 1
        done
        log "${candidate} did not receive a global IP address; checking the next Ethernet device."
    done

    fail "No usable Ethernet device is connected. Reconnect a source NIC and rerun preparation; existing systemd-networkd configuration was left unchanged."
}

if [[ "${network_backend}" == "wicked" ]]; then
    if command -v wicked >/dev/null 2>&1; then
        ensure_usable_wicked_connection
    else
        log "wicked is not installed yet; retaining the existing network configuration while packages are updated."
    fi
elif [[ "${network_backend}" == "systemd-networkd" ]]; then
    ensure_usable_networkd_connection
elif command -v nmcli >/dev/null 2>&1; then
    ensure_usable_ethernet_connection
else
    log "NetworkManager is not installed yet; retaining the existing network configuration while packages are updated."
fi

log "Updating ${PRETTY_NAME:-${os_id} ${os_version}} using ${package_manager}."
log "Detected ${network_backend} for network configuration."
install_packages
configure_rhel_qemu_guest_agent_rpcs

if wicked_owns_network_service; then
    network_backend="wicked"
elif networkd_owns_ethernet; then
    network_backend="systemd-networkd"
fi

configure_network_manager() {
    local config_path="/etc/NetworkManager/conf.d/20-organesson-no-auto-default.conf"

    if [[ -e "${config_path}" ]]; then
        [[ -f "${config_path}" && ! -L "${config_path}" ]] || fail "Refusing to replace a non-regular NetworkManager configuration at ${config_path}."
        grep -Fqx '# Managed by og-prep-linux.sh.' "${config_path}" || fail "Refusing to replace an unmanaged NetworkManager configuration at ${config_path}."
    fi

    ensure_usable_ethernet_connection
    install -d -o root -g root -m 0755 "${config_path%/*}"
    cat > "${config_path}" <<'EOF'
# Managed by og-prep-linux.sh.
[main]
no-auto-default=*
EOF
    chown root:root "${config_path}"
    chmod 0644 "${config_path}"

    nmcli general reload
    log "Enabled NetworkManager for deployment NIC setup and disabled automatic fallback profiles."
}

configure_wicked() {
    ensure_usable_wicked_connection
    log "Preserved wicked and its existing interface configuration during source preparation."
}

configure_networkd() {
    ensure_usable_networkd_connection
    log "Preserved systemd-networkd and its existing interface configuration during source preparation."
}

if [[ "${network_backend}" == "wicked" ]]; then
    configure_wicked
elif [[ "${network_backend}" == "systemd-networkd" ]]; then
    configure_networkd
else
    configure_network_manager
fi

log "Enabling and starting QEMU Guest Agent."
enable_service qemu-guest-agent
if command -v systemctl >/dev/null 2>&1; then
    agent_pid="$(systemctl show --property=MainPID --value qemu-guest-agent.service)"
else
    agent_pid="$(pidof qemu-ga | awk '{print $1}')"
fi
[[ "${agent_pid}" =~ ^[1-9][0-9]*$ ]] || fail "QEMU Guest Agent has no running process."
agent_uid="$(awk '/^Uid:/ { print $3 }' "/proc/${agent_pid}/status")"
[[ "${agent_uid}" == "0" ]] || fail "QEMU Guest Agent is not running as root (effective UID: ${agent_uid:-unknown})."
configure_selinux_qemu_guest_agent
verify_apparmor_qemu_guest_agent

log "Removing cloned machine identity."
if [[ -e /etc/machine-id ]]; then
    truncate --size 0 /etc/machine-id
fi
if [[ -e /var/lib/dbus/machine-id && ! -L /var/lib/dbus/machine-id ]]; then
    rm --force /var/lib/dbus/machine-id
fi
rm --force /etc/ssh/ssh_host_*
rm --force /var/lib/systemd/random-seed

[[ ! -e /etc/machine-id || ! -s /etc/machine-id ]] || fail "Could not clear /etc/machine-id."

log "Preparation complete for ${PRETTY_NAME:-${os_id} ${os_version}}."
log "Delete this script, then shut down this source VM without rebooting it before cloning."
