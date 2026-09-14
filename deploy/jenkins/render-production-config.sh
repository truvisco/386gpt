#!/bin/sh
set -eu
set +x

destination=${1:?usage: render-production-config.sh DESTINATION}
etcd_env_file=${ETCD_ENV_FILE:-/etc/etcd/jenkins.env}
prefix=${ETCD_PREFIX:-/prod/386gpt}

if [ ! -r "$etcd_env_file" ]; then
    echo "cannot read etcd environment file: $etcd_env_file" >&2
    exit 1
fi

set -a
# shellcheck disable=SC1090
. "$etcd_env_file"
set +a

endpoints=${ETCDCTL_ENDPOINTS:-${ETCD_ENDPOINTS:-}}
if [ -n "${ETCDCTL_USER:-}" ]; then
    authentication=$ETCDCTL_USER
elif [ -n "${ETCD_USER:-}" ] && [ -n "${ETCD_PASSWORD:-}" ]; then
    authentication=$ETCD_USER:$ETCD_PASSWORD
else
    authentication=
fi

if [ -z "$endpoints" ] || [ -z "$authentication" ]; then
    echo "etcd endpoints or authentication are missing from $etcd_env_file" >&2
    exit 1
fi

# etcdctl rejects an exported ETCDCTL_* value when the matching explicit flag
# is also present. Keep the captured values local and remove credentials from
# the environment inherited by child processes.
unset ETCDCTL_ENDPOINTS ETCDCTL_USER ETCD_ENDPOINTS ETCD_USER ETCD_PASSWORD

umask 077
mkdir -p "$destination"
config_file=$destination/hermes.yaml

etcdctl --command-timeout=10s \
    --endpoints="$endpoints" \
    --user="$authentication" \
    get "$prefix/hermes-agent-config" \
    --print-value-only > "$config_file"

if [ ! -s "$config_file" ]; then
    echo "missing required etcd key: $prefix/hermes-agent-config" >&2
    exit 1
fi

chmod 600 "$config_file"
echo "Rendered Hermes Agent production configuration from $prefix (value hidden)."
