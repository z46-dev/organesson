#!/usr/bin/env bash
set -euo pipefail

dnf -y update
systemctl enable --now qemu-guest-agent
touch /var/lib/organesson-bootstrap-complete
