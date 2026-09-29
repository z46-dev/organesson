#!/usr/bin/env bash
set -euo pipefail

# This script is intended to run directly from a temporary, read-only ISO.
# Organesson must not copy this script into the guest filesystem.
install -d -m 0755 /var/lib/organesson
date --iso-8601=seconds > /var/lib/organesson/first-time-setup-complete
