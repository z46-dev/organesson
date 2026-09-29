#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 ]]; then
    echo "usage: $0 NODE VM_ID COMMAND [ARGUMENT ...]" >&2
    exit 64
fi

node_name="$1"
vm_id="$2"
shift 2

# Run this only through Organesson's audited PVE execution service. qm guest exec
# runs as the guest's root/SYSTEM account when its agent supports guest-exec.
exec ssh -- "root@${node_name}" qm guest exec "$vm_id" -- "$@"
