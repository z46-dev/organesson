#!/usr/bin/env bash
set -Eeuo pipefail

tls_directory_file="/etc/organesson/tls-directory"
[[ -r "${tls_directory_file}" ]] || exit 1
IFS= read -r tls_directory < "${tls_directory_file}"
[[ "${tls_directory}" =~ ^/[A-Za-z0-9._/-]+$ && "${tls_directory}" != "/" && "/${tls_directory#/}/" != *"/../" ]] || exit 1
[[ -s "${tls_directory}/server.crt" && -s "${tls_directory}/server.key" ]] || exit 1

chown root:organesson "${tls_directory}/server.crt" "${tls_directory}/server.key"
chmod 0644 "${tls_directory}/server.crt"
chmod 0640 "${tls_directory}/server.key"

if systemctl is-active --quiet nginx.service; then
    systemctl reload nginx.service
fi

if systemctl is-active --quiet organesson.service; then
    systemctl restart organesson.service
fi
