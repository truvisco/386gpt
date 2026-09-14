#!/bin/sh
set -eu
set +x

release_id=${1:?usage: install-release.sh RELEASE_ID STAGING_DIRECTORY}
staging=${2:?usage: install-release.sh RELEASE_ID STAGING_DIRECTORY}

case "$release_id" in
    *[!A-Za-z0-9._-]*|'') echo "invalid release ID" >&2; exit 2 ;;
esac
case "$staging" in
    /tmp/386gpt-*) ;;
    *) echo "staging directory must be under /tmp/386gpt-*" >&2; exit 2 ;;
esac

test "$(id -u)" -eq 0 || { echo "this installer must run as root" >&2; exit 1; }
test -x "$staging/386gpt"
test -f "$staging/secrets/hermes.yaml"

app_user=grimlock
deploy_root=/var/www/vhosts/api-386gpt.truvis.co
release_root=$deploy_root/releases
release_dir=$release_root/$release_id
current_link=$deploy_root/current
config_dir=/etc/386gpt
previous_target=

cleanup() {
    rm -rf "$staging"
}
trap cleanup EXIT HUP INT TERM

id "$app_user" >/dev/null 2>&1 || { echo "missing deployment user: $app_user" >&2; exit 1; }

install -d -m 0755 -o "$app_user" -g "$app_user" "$deploy_root" "$release_root"
install -d -m 0750 -o "$app_user" -g "$app_user" "$deploy_root/shared"
install -d -m 0750 -o "$app_user" -g "$app_user" "$deploy_root/shared/data"
install -d -m 0755 -o "$app_user" -g "$app_user" "$deploy_root/logs"
install -d -m 0750 -o root -g "$app_user" "$config_dir"
install -d -m 0755 -o root -g root "$release_dir"
install -m 0755 -o root -g root "$staging/386gpt" "$release_dir/386gpt"
install -m 0640 -o root -g "$app_user" "$staging/secrets/hermes.yaml" "$config_dir/hermes.yaml"
install -m 0644 -o root -g root "$staging/386gpt.service" /etc/systemd/system/386gpt.service
install -m 0644 -o root -g root "$staging/api-386gpt.truvis.co.nginx.conf" /etc/nginx/sites-available/api-386gpt.truvis.co.conf
ln -sfn /etc/nginx/sites-available/api-386gpt.truvis.co.conf /etc/nginx/sites-enabled/api-386gpt.truvis.co.conf

nginx -t
systemctl daemon-reload
systemctl enable 386gpt.service >/dev/null

if [ -L "$current_link" ]; then
    previous_target=$(readlink -f "$current_link")
fi
ln -sfn "$release_dir" "$current_link"
systemctl restart 386gpt.service
systemctl reload nginx.service

healthy=false
attempt=1
while [ "$attempt" -le 20 ]; do
    if curl -fsS --max-time 3 http://127.0.0.1:20386/health >/dev/null; then
        healthy=true
        break
    fi
    sleep 1
    attempt=$((attempt + 1))
done

if [ "$healthy" != true ]; then
    journalctl -u 386gpt.service -n 50 --no-pager >&2 || true
    if [ -n "$previous_target" ] && [ -d "$previous_target" ]; then
        ln -sfn "$previous_target" "$current_link"
        systemctl restart 386gpt.service || true
    fi
    echo "386GPT failed its local health check; release rolled back when possible" >&2
    exit 1
fi

echo "386GPT release $release_id is healthy."
