#!/bin/sh
set -eu
# launchd restarts this supervisor if Docker Desktop is not yet available.
export PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin
checkout=${1:?checkout required}
settings=${2:?compose environment required}
docker info >/dev/null 2>&1 || exit 1
docker compose --env-file "$settings" -f "$checkout/deploy/isolation/compose.yaml" up -d --no-build --wait --wait-timeout 120
broker_pid=
cleanup() { if [ -n "$broker_pid" ]; then kill "$broker_pid" 2>/dev/null || true; fi; }
trap cleanup EXIT HUP INT TERM
config=$(dirname "$settings")
. "$settings"
if [ -f "$config/broker.key" ]; then
 "$HOME/.hermes/386gpt-runtime/venv/bin/python" "$checkout/deploy/isolation/tenant-broker.py" \
  --root "$config/accounts" --template "$config" --compose "$checkout/deploy/isolation/compose.yaml" \
  --key-file "$config/broker.key" --bind 127.0.0.1 --upstream "$UNSLOTH_UPSTREAM" &
 broker_pid=$!
fi
while docker info >/dev/null 2>&1; do
 if [ -n "$broker_pid" ]; then kill -0 "$broker_pid" 2>/dev/null || exit 1; fi
 sleep 30
done
exit 1
