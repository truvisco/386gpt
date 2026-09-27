#!/bin/sh
set -eu
# Dedicated code checkout; dependencies come from the existing host installation.
# Hermes itself supports shared venvs for isolated worktrees.
source_dir=${HERMES_SOURCE_DIR:-$HOME/.hermes/hermes-agent}
runtime_dir=${HERMES_RUNTIME_DIR:-$HOME/.hermes/386gpt-runtime}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
revision=498abb677ec39ea3ae9f8f5ed60e7def6bc47e70
test -x "$source_dir/venv/bin/python"
if [ ! -d "$runtime_dir/.git" ]; then
    git clone --no-hardlinks --no-checkout "$source_dir" "$runtime_dir"
    git -C "$runtime_dir" checkout --detach "$revision"
fi
test -e "$runtime_dir/venv" || ln -s "$source_dir/venv" "$runtime_dir/venv"
"$source_dir/venv/bin/python" "$script_dir/patch-hermes-runtime.py" "$runtime_dir"
echo "Dedicated Hermes code ready at $runtime_dir"
