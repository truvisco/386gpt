#!/bin/sh
set -eu
set +x

new_base_url=${1:?usage: update-hermes-base-url.sh BASE_URL}
etcd_env_file=${ETCD_ENV_FILE:-/etc/etcd/jenkins.env}
prefix=${ETCD_PREFIX:-/prod/386gpt}

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

unset ETCDCTL_ENDPOINTS ETCDCTL_USER ETCD_ENDPOINTS ETCD_USER ETCD_PASSWORD

source_file=$(mktemp)
updated_file=$(mktemp)
cleanup() {
    shred -u "$source_file" "$updated_file" 2>/dev/null || rm -f "$source_file" "$updated_file"
}
trap cleanup EXIT HUP INT TERM
umask 077

etcdctl --command-timeout=10s \
    --endpoints="$endpoints" \
    --user="$authentication" \
    get "$prefix/hermes-config" \
    --print-value-only > "$source_file"

if [ ! -s "$source_file" ]; then
    echo "missing required etcd key: $prefix/hermes-config" >&2
    exit 1
fi

awk -v url="$new_base_url" '
    /^[[:space:]]+base_url:[[:space:]]/ {
        count++
        sub(/base_url:.*/, "base_url: " url)
    }
    { print }
    END { if (count != 1) exit 42 }
' "$source_file" > "$updated_file" || {
    echo "expected exactly one base_url in the Hermes configuration" >&2
    exit 1
}

etcdctl --command-timeout=10s \
    --endpoints="$endpoints" \
    --user="$authentication" \
    put "$prefix/hermes-config" < "$updated_file" >/dev/null

echo "Updated the Hermes base URL in $prefix/hermes-config (credentials hidden)."
