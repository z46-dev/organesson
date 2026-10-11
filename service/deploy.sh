#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVICE_USER="organesson"
readonly CONFIG_DIR="/etc/organesson"
readonly CONFIG_FILE="${CONFIG_DIR}/config.toml"
readonly TLS_DIR="${CONFIG_DIR}/tls"
readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"

as_root() {
    if [[ "${EUID}" -eq 0 ]]; then
        "$@"
    else
        sudo "$@"
    fi
}

fail() {
    printf '\n[organesson] ERROR: %s\n' "$*" >&2
    exit 1
}

prompt_install_directory() {
    local answer

    read -r -e -p "Install directory [/opt/organesson]: " answer
    INSTALL_DIR="${answer:-/opt/organesson}"
    [[ "${INSTALL_DIR}" == /* && "${INSTALL_DIR}" != "/" && "${INSTALL_DIR}" != "/opt" && "${INSTALL_DIR}" != "/home" ]] || fail "Choose a dedicated absolute install directory."
    [[ "${INSTALL_DIR}" =~ ^/[A-Za-z0-9._/-]+$ && "/${INSTALL_DIR#/}/" != *"/../"* ]] || fail "Install directory may contain only path-safe characters and cannot contain parent-directory components."
}

install_dependencies() {
    as_root dnf install -y git golang nginx openssl python3 policycoreutils-python-utils curl
    if ! command -v bun >/dev/null 2>&1; then
        printf '[organesson] Installing Bun for the current login user from bun.sh.\n'
        curl -fsSL https://bun.sh/install | bash
        export PATH="${HOME}/.bun/bin:${PATH}"
    fi
    command -v go >/dev/null 2>&1 || fail "Go installation failed."
    command -v bun >/dev/null 2>&1 || fail "Bun installation failed."
}

choose_editor() {
    local editor

    for editor in nano vim vi "${VISUAL:-}" "${EDITOR:-}" editor; do
        if [[ -n "${editor}" ]] && command -v "${editor}" >/dev/null 2>&1; then
            SELECTED_EDITOR="$(command -v "${editor}")"
            return
        fi
    done
    fail "No editor found. Install nano, vim, or vi and rerun."
}

prepare_tls() {
    local hostname="${PUBLIC_HOSTNAME}"

    if as_root test -s "${TLS_DIR}/server.crt" && as_root test -s "${TLS_DIR}/server.key"; then
        as_root openssl x509 -in "${TLS_DIR}/server.crt" -checkhost "${hostname}" -noout >/dev/null || fail "The existing TLS certificate does not cover ${hostname}. Replace the certificate and key in ${TLS_DIR} before continuing."
        return
    fi

    [[ "${hostname}" =~ ^[A-Za-z0-9._-]+$ ]] || fail "Enter a hostname without a scheme or path."
    as_root install -d -o root -g "${SERVICE_USER}" -m 0750 "${TLS_DIR}"
    printf '[organesson] Creating a self-signed certificate. Replace it with a trusted certificate before public use.\n'
    as_root openssl req -x509 -newkey rsa:3072 -sha256 -days 365 -nodes \
        -keyout "${TLS_DIR}/server.key" -out "${TLS_DIR}/server.crt" \
        -subj "/CN=${hostname}" \
        -addext "subjectAltName=DNS:${hostname},DNS:localhost,IP:127.0.0.1,IP:::1"
    as_root chown "root:${SERVICE_USER}" "${TLS_DIR}/server.crt" "${TLS_DIR}/server.key"
    as_root chmod 0644 "${TLS_DIR}/server.crt"
    as_root chmod 0640 "${TLS_DIR}/server.key"
}

prepare_config() {
    local answer
    local generated_key
    local staged_config

    as_root install -d -o root -g "${SERVICE_USER}" -m 0750 "${CONFIG_DIR}"
    as_root install -d -o "${SERVICE_USER}" -g "${SERVICE_USER}" -m 0750 /var/lib/organesson
    if ! as_root test -f "${CONFIG_FILE}"; then
        generated_key="$(openssl rand -base64 32 | tr -d '\n')"
        staged_config="$(mktemp)"
        sed "s|GENERATED_BY_INSTALLER|${generated_key}|" "${REPOSITORY_ROOT}/service/config.toml.example" > "${staged_config}"
        as_root install -o root -g "${SERVICE_USER}" -m 0640 "${staged_config}" "${CONFIG_FILE}"
        rm -f -- "${staged_config}"
    else
        printf '[organesson] Preserving existing %s.\n' "${CONFIG_FILE}"
    fi

    read -r -p "Open backend config.toml in an editor now? [Y/n] " answer
    if [[ ! "${answer}" =~ ^[Nn]$ ]]; then
        choose_editor
        as_root "${SELECTED_EDITOR}" "${CONFIG_FILE}"
    fi

    if ! config_values="$(as_root python3 - "${CONFIG_FILE}" <<'PY'
import base64
import pathlib
import sys
import tomllib
import urllib.parse

with pathlib.Path(sys.argv[1]).open("rb") as config_file:
    config = tomllib.load(config_file)

web_server = config.get("web_server", {})
database = config.get("database", {})
proxmox = config.get("proxmox", {})
authentication = config.get("authentication", {})
address = urllib.parse.urlsplit("//" + web_server.get("address", ""))
if address.hostname != "127.0.0.1" or not address.port:
    raise SystemExit("web_server.address must use loopback and an explicit port, e.g. 127.0.0.1:8443.")
if address.port in (80, 443):
    raise SystemExit("web_server.address cannot use 80 or 443 because Nginx uses those public ports.")
if web_server.get("tls_dir") != "/etc/organesson/tls":
    raise SystemExit("Keep web_server.tls_dir at /etc/organesson/tls.")
database_file = pathlib.Path(database.get("file", "")).resolve()
if not database_file.is_relative_to(pathlib.Path("/var/lib/organesson").resolve()):
    raise SystemExit("database.file must be under /var/lib/organesson/.")
if not proxmox.get("api_url", "").startswith("https://"):
    raise SystemExit("Set proxmox.api_url to an HTTPS Proxmox API URL.")
if not proxmox.get("api_token_id") or not proxmox.get("api_token_secret"):
    raise SystemExit("Set the Proxmox API token ID and secret.")
try:
    encryption_key = base64.b64decode(authentication.get("encryption_key", ""), validate=True)
except Exception as error:
    raise SystemExit("authentication.encryption_key must be base64-encoded 32-byte key material.") from error
if len(encryption_key) != 32:
    raise SystemExit("authentication.encryption_key must decode to exactly 32 bytes.")
if config.get("development", {}).get("enable_test_fixtures", False):
    raise SystemExit("development.enable_test_fixtures must be false.")
print(address.port)
print(database_file)
PY
)"; then
        fail "Config validation failed; edit ${CONFIG_FILE} and rerun."
    fi
    mapfile -t config_values <<< "${config_values}"
    BACKEND_PORT="${config_values[0]}"
    DATABASE_FILE="${config_values[1]}"
    as_root test -s "${TLS_DIR}/server.crt" || fail "TLS certificate is missing."
    as_root test -s "${TLS_DIR}/server.key" || fail "TLS key is missing."
}

setup_selinux() {
    local current_type

    selinuxenabled || return 0
    current_type="$(semanage port -l | awk -v target="${BACKEND_PORT}" '$2 == "tcp" { for (column = 3; column <= NF; column++) { gsub(/,/, "", $column); if ($column == target) { print $1; exit } } }')"
    if [[ -z "${current_type}" ]]; then
        as_root semanage port -a -t http_port_t -p tcp "${BACKEND_PORT}"
    elif [[ "${current_type}" != "http_port_t" ]]; then
        fail "Backend port ${BACKEND_PORT} has SELinux type ${current_type}; select a port labeled http_port_t."
    fi
    as_root setsebool -P httpd_can_network_connect 1
    as_root semanage fcontext -a -t httpd_sys_content_t "${INSTALL_DIR}/frontend(/.*)?" 2>/dev/null || as_root semanage fcontext -m -t httpd_sys_content_t "${INSTALL_DIR}/frontend(/.*)?"
    as_root semanage fcontext -a -t cert_t "${TLS_DIR}(/.*)?" 2>/dev/null || as_root semanage fcontext -m -t cert_t "${TLS_DIR}(/.*)?"
}

build_application() {
    BUILD_DIR="$(mktemp -d)"
    trap 'rm -rf -- "${BUILD_DIR}"' EXIT

    printf '[organesson] Building Go backend.\n'
    (cd "${REPOSITORY_ROOT}" && GOTOOLCHAIN=auto go build -trimpath -o "${BUILD_DIR}/organesson" ./backend)
    printf '[organesson] Checking and building frontend.\n'
    (cd "${REPOSITORY_ROOT}/frontend" && bun install --frozen-lockfile && bun run typecheck && bun run test && bun run build)

    as_root install -d -o root -g root -m 0755 "${INSTALL_DIR}/bin" "${INSTALL_DIR}/frontend"
    as_root install -o root -g root -m 0755 "${BUILD_DIR}/organesson" "${INSTALL_DIR}/bin/organesson.new"
    as_root mv -f "${INSTALL_DIR}/bin/organesson.new" "${INSTALL_DIR}/bin/organesson"
    as_root cp -a "${REPOSITORY_ROOT}/frontend/dist/." "${INSTALL_DIR}/frontend/"
    as_root chown -R root:root "${INSTALL_DIR}/bin" "${INSTALL_DIR}/frontend"
    as_root find "${INSTALL_DIR}/bin" "${INSTALL_DIR}/frontend" -type d -exec chmod 0755 {} +
    as_root find "${INSTALL_DIR}/frontend" -type f -exec chmod 0644 {} +
}

configure_services() {
    as_root tee /etc/systemd/system/organesson.service >/dev/null <<EOF
[Unit]
Description=Organesson infrastructure management service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=/var/lib/organesson
ExecStart=${INSTALL_DIR}/bin/organesson --config ${CONFIG_FILE}
Restart=on-failure
RestartSec=5s
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=/var/lib/organesson
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

    as_root tee /etc/nginx/conf.d/organesson.conf >/dev/null <<EOF
map \$http_upgrade \$organesson_connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 80;
    server_name ${PUBLIC_HOSTNAME};
    return 301 https://${PUBLIC_HOSTNAME}\$request_uri;
}

server {
    listen 443 ssl;
    server_name ${PUBLIC_HOSTNAME};
    root ${INSTALL_DIR}/frontend;
    index index.html;
    client_max_body_size 24m;
    ssl_certificate ${TLS_DIR}/server.crt;
    ssl_certificate_key ${TLS_DIR}/server.key;
    ssl_protocols TLSv1.2 TLSv1.3;

    location /api/ {
        proxy_pass https://127.0.0.1:${BACKEND_PORT};
        proxy_http_version 1.1;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$organesson_connection_upgrade;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
        proxy_ssl_server_name on;
        proxy_ssl_name ${PUBLIC_HOSTNAME};
        proxy_ssl_trusted_certificate ${TLS_DIR}/server.crt;
        proxy_ssl_verify on;
    }

    location / {
        try_files \$uri \$uri/ /index.html;
    }
}
EOF

    as_root restorecon -RF "${INSTALL_DIR}/frontend" "${TLS_DIR}" 2>/dev/null || true
    as_root nginx -t
    as_root systemctl daemon-reload
    as_root systemctl enable --now nginx
    as_root systemctl enable --now organesson.service
    as_root systemctl restart organesson.service
    as_root systemctl reload nginx
}

verify_service() {
    local attempt

    for attempt in {1..15}; do
        if curl --silent --show-error --fail --insecure --resolve "${PUBLIC_HOSTNAME}:443:127.0.0.1" "https://${PUBLIC_HOSTNAME}/api/v1/auth/status" >/dev/null 2>&1; then
            printf '\n[organesson] Ready at https://%s/\n' "${PUBLIC_HOSTNAME}"
            return
        fi
        sleep 2
    done

    as_root systemctl --no-pager --full status organesson.service || true
    as_root journalctl -u organesson.service -n 60 --no-pager || true
    fail "Service health check failed."
}

main() {
    local answer
    local hostname_answer
    local fedora_version

    [[ -d "${REPOSITORY_ROOT}/backend" && -d "${REPOSITORY_ROOT}/frontend" ]] || fail "Run this script from a complete Organesson git checkout."
    [[ -r /etc/os-release ]] || fail "This installer supports Fedora Server 44."
    . /etc/os-release
    fedora_version="${VERSION_ID:-}"
    [[ "${ID:-}" == "fedora" && "${fedora_version%%.*}" == "44" ]] || fail "This installer supports Fedora Server 44; detected ${PRETTY_NAME:-an unknown operating system}."
    command -v sudo >/dev/null 2>&1 || [[ "${EUID}" -eq 0 ]] || fail "Install sudo or run as root."
    prompt_install_directory
    if [[ "${EUID}" -ne 0 ]]; then
        read -r -p "Install or update Organesson and enable its services? [y/N] " answer
        [[ "${answer}" =~ ^[Yy]$ ]] || exit 0
    fi

    install_dependencies
    if ! id "${SERVICE_USER}" >/dev/null 2>&1; then
        as_root useradd --system --home-dir /var/lib/organesson --create-home --shell /sbin/nologin "${SERVICE_USER}"
    fi
    as_root groupadd --force "${SERVICE_USER}"
    as_root usermod --gid "${SERVICE_USER}" "${SERVICE_USER}"
    PUBLIC_HOSTNAME="$(hostname -f 2>/dev/null || hostname)"
    [[ "${PUBLIC_HOSTNAME}" == *.* ]] || PUBLIC_HOSTNAME="localhost"
    read -r -p "Hostname for the web UI [${PUBLIC_HOSTNAME}]: " hostname_answer
    PUBLIC_HOSTNAME="${hostname_answer:-${PUBLIC_HOSTNAME}}"
    [[ "${PUBLIC_HOSTNAME}" =~ ^[A-Za-z0-9._-]+$ ]] || fail "Enter a DNS hostname without a scheme or path."
    prepare_tls
    prepare_config

    as_root chown root:"${SERVICE_USER}" "${CONFIG_FILE}" "${TLS_DIR}/server.crt" "${TLS_DIR}/server.key"
    as_root chmod 0640 "${CONFIG_FILE}" "${TLS_DIR}/server.key"
    as_root chmod 0644 "${TLS_DIR}/server.crt"
    as_root install -d -o "${SERVICE_USER}" -g "${SERVICE_USER}" -m 0750 "$(dirname -- "${DATABASE_FILE}")"
    if as_root test -f "${DATABASE_FILE}"; then
        as_root chown "${SERVICE_USER}:${SERVICE_USER}" "${DATABASE_FILE}"
    fi
    setup_selinux
    build_application
    configure_services
    verify_service
}

main "$@"
