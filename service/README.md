# Fedora Server deployment

`deploy.sh` builds Organesson from the checkout, installs the backend and production frontend, configures Nginx as the HTTPS frontend/API/WebSocket proxy, and enables systemd services. It is intended to run on Fedora Server 44 from a clean clone or an updated checkout.

```bash
git clone <organesson-repository-url>
cd organesson
./service/deploy.sh
```

Run it as a normal account with `sudo` access. The script installs the Fedora build/runtime packages, installs Bun for the current account if needed, prompts for an install directory (default `/opt/organesson`), creates the `organesson` system account, and builds the Go backend and React frontend. It runs the frontend type check and tests before installing. On subsequent runs it preserves the existing config, database, and TLS files while rebuilding the app and restarting its services.

The script opens `/etc/organesson/config.toml` in the first available editor, preferring `nano`, then `vim`, then `vi`. Configure the Proxmox HTTPS API URL and API token before continuing. The installer generates a persistent 32-byte authentication encryption key when it first creates the config; keep that key with the database backups because stored LDAP secrets cannot be decrypted without it. Do not enable development test fixtures on a live instance.

The default TLS certificate is self-signed so the service can start immediately. That is suitable only for an isolated lab or initial smoke test; install a trusted certificate as `/etc/organesson/tls/server.crt` and its matching private key as `/etc/organesson/tls/server.key` before public use, then rerun the script. The private key is readable only by root and the `organesson` service group. Nginx serves the UI on ports 80/443, redirects HTTP to HTTPS, and proxies API and console WebSocket requests to the backend on loopback port 8443.

The backend runs as the unprivileged `organesson` system user under `organesson.service`; Nginx is enabled through Fedora's packaged `nginx.service`. The backend unit has only `CAP_NET_BIND_SERVICE` available, so an administrator can configure a low backend port if needed. The default backend port is non-privileged. SELinux remains enforcing: the installer labels the static files and TLS files, labels the backend port for Nginx proxy access, and enables Fedora's `httpd_can_network_connect` boolean so Nginx can reach the loopback API. It refuses to relabel a backend port already assigned to another SELinux type.

Open TCP ports 80 and 443 in the host firewall and point the chosen DNS name at the Fedora host. The installer prints the service URL and checks the local HTTPS API status endpoint after starting. Useful follow-up commands:

```bash
sudo systemctl status organesson nginx
sudo journalctl -u organesson -f
sudo systemctl restart organesson
```

For updates, pull the desired revision in the checkout and run `./service/deploy.sh` again. The script does not back up or migrate your database between hosts; back up `/var/lib/organesson/organesson.db` and `/etc/organesson/config.toml` together before moving an installation.
