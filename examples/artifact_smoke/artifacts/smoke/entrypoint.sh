#!/usr/bin/env bash
set -eu

expected="artifact payload delivered through QEMU Guest Agent"
actual="$(<payload/expected.txt)"

if [[ "$actual" != "$expected" ]]; then
    echo "artifact payload did not match the expected content" >&2
    exit 1
fi

echo "Organesson artifact smoke verified its delivered payload"
