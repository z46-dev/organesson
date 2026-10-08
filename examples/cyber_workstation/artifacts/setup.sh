#!/usr/bin/env bash
set -euo pipefail

username="ogusr"
if getent passwd "${username}" >/dev/null; then
    usermod --shell /bin/bash --append --groups wheel "${username}"
else
    useradd --create-home --shell /bin/bash --groups wheel "${username}"
fi

printf '%s:%s\n' "${username}" "${username}" | chpasswd

user_home="$(getent passwd "${username}" | awk -F: '{ print $6 }')"
if [[ -z "${user_home}" || ! -d "${user_home}" ]]; then
    echo "Could not find the home directory for ${username}." >&2
    exit 1
fi

# Prevent GNOME's per-user first-login setup from running for the created account.
install -d -o "${username}" -g "${username}" -m 0750 "${user_home}/.config"
printf 'yes\n' > "${user_home}/.config/gnome-initial-setup-done"
chown "${username}:${username}" "${user_home}/.config/gnome-initial-setup-done"
chmod 0644 "${user_home}/.config/gnome-initial-setup-done"

# GDM starts the system account-creation setup when Fedora has no regular users.
gdm_config="/etc/gdm/custom.conf"
install -d -o root -g root -m 0755 "${gdm_config%/*}"
temporary_config="$(mktemp "${gdm_config}.XXXXXX")"
if [[ -f "${gdm_config}" ]]; then
    awk '
        BEGIN {
            in_daemon = 0
            daemon_seen = 0
            setting_written = 0
        }
        /^\[[^]]+\][[:space:]]*$/ {
            if (in_daemon && !setting_written) {
                print "InitialSetupEnable=False"
                setting_written = 1
            }
            in_daemon = ($0 == "[daemon]")
            if (in_daemon) {
                daemon_seen = 1
            }
            print
            next
        }
        in_daemon && /^[[:space:]]*InitialSetupEnable[[:space:]]*=/ {
            if (!setting_written) {
                print "InitialSetupEnable=False"
                setting_written = 1
            }
            next
        }
        {
            print
        }
        END {
            if (in_daemon && !setting_written) {
                print "InitialSetupEnable=False"
            }
            if (!daemon_seen) {
                print ""
                print "[daemon]"
                print "InitialSetupEnable=False"
            }
        }
    ' "${gdm_config}" > "${temporary_config}"
else
    printf '[daemon]\nInitialSetupEnable=False\n' > "${temporary_config}"
fi
chown root:root "${temporary_config}"

chmod 0644 "${temporary_config}"
mv -- "${temporary_config}" "${gdm_config}"

# Let the guest-setup command finish, then boot GDM with the new account and config.
if systemctl is-active --quiet gdm.service; then
    systemd-run --on-active=15s /usr/bin/systemctl reboot
    echo "Scheduled a reboot so GDM starts at the normal login screen."
fi

echo "Created ${username} and disabled GNOME's account-creation setup."
