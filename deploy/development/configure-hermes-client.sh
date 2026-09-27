#!/bin/sh
set -eu
set +x

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
sh "$script_dir/../production/prepare-hermes-runtime.sh"
exec "$HOME/.hermes/386gpt-runtime/venv/bin/python" "$script_dir/configure-local-hermes.py"
