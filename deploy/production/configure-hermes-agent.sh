#!/bin/sh
set -eu
set +x

etcd_env_file=${ETCD_ENV_FILE:-/etc/etcd/jenkins.env}
etcd_ssh_host=${ETCD_SSH_HOST:-brain}
prefix=${ETCD_PREFIX:-/prod/386gpt}
hermes_host=${HERMES_API_HOST:-100.74.13.43}
hermes_port=${HERMES_API_PORT:-8643}
profile_name=${HERMES_PROFILE:-386gpt}
service_name=${HERMES_SERVICE_NAME:-hermes-gateway-386gpt.service}
profile_dir=/home/grimlock/.hermes/profiles/$profile_name
hermes_python=/home/grimlock/.hermes/hermes-agent/venv/bin/python

test "$(id -u)" -eq 0 || { echo "this script must run as root" >&2; exit 1; }

case "$prefix:$etcd_env_file:$etcd_ssh_host" in
    *[!A-Za-z0-9_./:-]*) echo "unsafe etcd configuration value" >&2; exit 1 ;;
esac

if [ -r "$etcd_env_file" ]; then
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

    etcd_get() {
        etcdctl --command-timeout=10s --endpoints="$endpoints" --user="$authentication" \
            get "$1" --print-value-only
    }
    etcd_put() {
        etcdctl --command-timeout=10s --endpoints="$endpoints" --user="$authentication" \
            put "$1"
    }
else
    etcd_remote="set -a; . $etcd_env_file; set +a; etcdctl --command-timeout=10s"
    etcd_get() {
        sudo -u grimlock env HOME=/home/grimlock ssh -o BatchMode=yes "$etcd_ssh_host" \
            "sudo /bin/sh -c '$etcd_remote get $1 --print-value-only'"
    }
    etcd_put() {
        sudo -u grimlock env HOME=/home/grimlock ssh -o BatchMode=yes "$etcd_ssh_host" \
            "sudo /bin/sh -c '$etcd_remote put $1'"
    }
fi

api_key=$(etcd_get "$prefix/hermes-api-key")
if [ -z "$api_key" ]; then
    api_key=$(openssl rand -hex 32)
    printf '%s' "$api_key" | etcd_put "$prefix/hermes-api-key" >/dev/null
    echo "Created the Hermes API credential in etcd."
fi

case "$api_key" in
    *[!A-Za-z0-9._-]*|'') echo "the Hermes API credential contains unsafe environment-file characters" >&2; exit 1 ;;
esac

runtime_dir=/etc/hermes-agent
environment_file=$runtime_dir/386gpt-profile-api.env
legacy_override=/etc/systemd/system/hermes-gateway.service.d/386gpt-api.conf
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM

umask 077
etcd_get "$prefix/hermes-config" > "$temp_dir/provider.yaml"
if [ ! -s "$temp_dir/provider.yaml" ]; then
    echo "missing required etcd key: $prefix/hermes-config" >&2
    exit 1
fi

awk '
    /^[[:space:]]+base_url:[[:space:]]/ {
        count++
        sub(/base_url:.*/, "base_url: http://127.0.0.1:30001/v1")
    }
    { print }
    END { if (count != 1) exit 42 }
' "$temp_dir/provider.yaml" > "$temp_dir/profile.yaml" || {
    echo "expected exactly one provider base_url in $prefix/hermes-config" >&2
    exit 1
}

printf '%s\n' \
    'API_SERVER_ENABLED=true' \
    "API_SERVER_HOST=$hermes_host" \
    "API_SERVER_PORT=$hermes_port" \
    "API_SERVER_KEY=$api_key" > "$temp_dir/386gpt-api.env"

printf '%s\n' \
    '[Unit]' \
    'Description=Hermes Agent Gateway - 386GPT profile' \
    'After=network-online.target tailscaled.service' \
    'Wants=network-online.target tailscaled.service' \
    'StartLimitIntervalSec=0' \
    '' \
    '[Service]' \
    'Type=simple' \
    'User=grimlock' \
    'Group=grimlock' \
    "WorkingDirectory=$profile_dir" \
    "EnvironmentFile=$environment_file" \
    'Environment=HOME=/home/grimlock' \
    'Environment=USER=grimlock' \
    'Environment=LOGNAME=grimlock' \
    'Environment=PATH=/home/grimlock/.hermes/hermes-agent/venv/bin:/home/grimlock/.hermes/hermes-agent/node_modules/.bin:/usr/bin:/home/grimlock/.local/bin:/home/grimlock/.cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
    "ExecStart=$hermes_python -m hermes_cli.main -p $profile_name gateway run" \
    'Restart=always' \
    'RestartSec=5' \
    'RestartForceExitStatus=75' \
    'KillMode=mixed' \
    'KillSignal=SIGTERM' \
    'TimeoutStopSec=210' \
    'StandardOutput=journal' \
    'StandardError=journal' \
    '' \
    '[Install]' \
    'WantedBy=multi-user.target' > "$temp_dir/hermes-gateway-386gpt.service"

printf '%s\n' \
    'agent:' \
    "  base_url: http://$hermes_host:$hermes_port" \
    "  api_key: $api_key" \
    '  session_key: agent:main:386gpt:dm:owner' > "$temp_dir/hermes.yaml"

install -d -m 0750 -o root -g grimlock "$runtime_dir"
install -m 0640 -o root -g grimlock "$temp_dir/386gpt-api.env" "$environment_file"
if [ ! -d "$profile_dir" ]; then
    sudo -u grimlock env HOME=/home/grimlock "$hermes_python" -m hermes_cli.main \
        profile create "$profile_name" --no-alias \
        --description "Private Hermes Agent profile for the 386GPT web chat."
fi
install -m 0600 -o grimlock -g grimlock "$temp_dir/profile.yaml" "$profile_dir/config.yaml"
install -m 0644 -o root -g root "$temp_dir/hermes-gateway-386gpt.service" "/etc/systemd/system/$service_name"
etcd_put "$prefix/hermes-agent-config" < "$temp_dir/hermes.yaml" >/dev/null

systemctl daemon-reload
systemctl enable "$service_name" >/dev/null
systemctl restart "$service_name"
if [ -f "$legacy_override" ]; then
    rm -f "$legacy_override"
    systemctl daemon-reload
    systemctl restart hermes-gateway.service
fi

attempt=1
while [ "$attempt" -le 30 ]; do
    if curl -fsS --max-time 3 \
        -H "Authorization: Bearer $api_key" \
        "http://$hermes_host:$hermes_port/v1/capabilities" >/dev/null; then
        echo "Hermes Agent API is healthy on the private Tailscale address."
        exit 0
    fi
    sleep 2
    attempt=$((attempt + 1))
done

journalctl -u "$service_name" -n 80 --no-pager >&2 || true
echo "Hermes Agent API did not become healthy" >&2
exit 1
