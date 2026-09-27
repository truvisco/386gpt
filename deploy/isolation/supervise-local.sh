#!/bin/sh
set -eu
# launchd restarts this supervisor if Docker Desktop is not yet available.
export PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin
checkout=${1:?checkout required}
settings=${2:?compose environment required}
docker info >/dev/null 2>&1 || exit 1
docker compose --env-file "$settings" -f "$checkout/deploy/isolation/compose.yaml" up -d --no-build --wait --wait-timeout 120
while docker info >/dev/null 2>&1; do sleep 30; done
exit 1
