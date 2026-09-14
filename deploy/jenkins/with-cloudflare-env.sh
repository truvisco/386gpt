#!/bin/sh
set -eu
set +x

if [ "$#" -eq 0 ]; then
    echo "usage: with-cloudflare-env.sh COMMAND [ARGUMENT ...]" >&2
    exit 2
fi

etcd_env_file=${ETCD_ENV_FILE:-/etc/etcd/jenkins.env}
shared_prefix=${SHARED_ETCD_PREFIX:-/prod/truvis.co}

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

get_value() {
    etcdctl --command-timeout=10s \
        --endpoints="$endpoints" \
        --user="$authentication" \
        get "$shared_prefix/$1" \
        --print-value-only
}

CLOUDFLARE_ACCOUNT_ID=$(get_value CLOUDFLARE_ACCOUNT_ID)
CLOUDFLARE_API_TOKEN=$(get_value CLOUDFLARE_API_TOKEN)
export CLOUDFLARE_ACCOUNT_ID CLOUDFLARE_API_TOKEN

if [ -z "$CLOUDFLARE_ACCOUNT_ID" ] || [ -z "$CLOUDFLARE_API_TOKEN" ]; then
    echo "Cloudflare account ID or API token is missing from $shared_prefix" >&2
    exit 1
fi

exec "$@"
