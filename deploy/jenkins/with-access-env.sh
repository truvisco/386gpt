#!/bin/sh
set -eu
set +x
# render-production-config.sh creates this alongside the gateway connection.
access_env=${ACCESS_ENV_FILE:-.deploy/release/secrets/access.env}
test -r "$access_env" || { echo 'Missing Access configuration; refusing unauthenticated deployment' >&2; exit 1; }
set -a
. "$access_env"
set +a
exec "$@"
