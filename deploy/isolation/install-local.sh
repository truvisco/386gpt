#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
settings=${ISOLATION_CONFIG:-$HOME/.config/386gpt-isolation}
profile=${HERMES_SOURCE_PROFILE:-$HOME/.hermes/profiles/386gpt-local}
python=${HERMES_MIGRATION_PYTHON:-$HOME/.hermes/386gpt-runtime/venv/bin/python}
cd "$root"
docker build -t 386gpt-hermes:isolated -f deploy/isolation/Dockerfile .
docker build -t 386gpt-boundary:isolated -f deploy/isolation/Dockerfile.proxy .
if [ ! -f "$settings/compose.env" ]; then
 "$python" deploy/isolation/prepare.py --profile "$profile" --output "$settings" --bind 127.0.0.1 --upstream http://100.74.13.43:8888
fi
if ! grep -q '^HERMES_STATE_VOLUME=' "$settings/compose.env"; then
 launchctl bootout "gui/$(id -u)/co.truvis.386gpt.hermes" 2>/dev/null || true
 "$python" deploy/isolation/snapshot.py "$profile" "$settings/migration"
 sh deploy/isolation/install-state.sh "$settings/migration" 386gpt-isolated-migrated_state
 printf '\nHERMES_STATE_VOLUME=386gpt-isolated-migrated_state\n' >> "$settings/compose.env"
fi
"$python" deploy/isolation/update-client.py "$HOME/.hermes/386gpt.yaml" "$settings/gateway.key" --runtime local --url http://127.0.0.1:8644
python3 deploy/isolation/workspace.py --project 386gpt-isolated --environment "$settings/compose.env" --workspace "$settings/workspace"
python3 deploy/isolation/install-local-supervisor.py "$settings/compose.env"
