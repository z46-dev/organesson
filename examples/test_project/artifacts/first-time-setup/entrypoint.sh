#!/usr/bin/env bash
set -euo pipefail

# Organesson stages this script only in a temporary /run workspace and removes it after execution.
install -d -m 0755 /var/lib/organesson
date --iso-8601=seconds > /var/lib/organesson/first-time-setup-complete
