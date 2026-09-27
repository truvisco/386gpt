#!/bin/sh
# Run on crash as the deployment operator (with sudo), after deploying Access + backend JWT enforcement.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
# A Cloudflare redirect alone is insufficient: verify the web1 origin itself rejects anonymous requests.
status=$(ssh -o BatchMode=yes web1 "curl -sS -o /dev/null -w '%{http_code}' -H 'Host: api-386gpt.truvis.co' http://127.0.0.1:20386/api/threads")
[ "$status" = 401 ] || { echo 'Origin authentication is not enforced; refusing cutover.' >&2; exit 1; }
settings=/etc/386gpt-isolation
python=/home/grimlock/.hermes/386gpt-runtime/venv/bin/python
profile=/home/grimlock/.hermes/profiles/386gpt
sudo test -s "$settings/compose.env"
docker image inspect 386gpt-hermes:isolated >/dev/null
docker image inspect 386gpt-boundary:isolated >/dev/null
if ! sudo grep -q '^HERMES_STATE_VOLUME=' "$settings/compose.env"; then
 sudo systemctl stop hermes-gateway-386gpt.service
 sudo "$python" deploy/isolation/snapshot.py "$profile" "$settings/migration"
 sudo sh deploy/isolation/install-state.sh "$settings/migration" 386gpt-isolated-migrated_state
 printf '\nHERMES_STATE_VOLUME=386gpt-isolated-migrated_state\n' | sudo tee -a "$settings/compose.env" >/dev/null
fi
# Archive overrides so the old ExecStart cannot override the container unit.
if sudo test -d /etc/systemd/system/hermes-gateway-386gpt.service.d; then
 sudo mv /etc/systemd/system/hermes-gateway-386gpt.service.d "$settings/old-systemd-dropins-$(date +%s)"
fi
sudo install -m 0644 deploy/isolation/hermes-container.service /etc/systemd/system/hermes-gateway-386gpt.service
sudo "$python" deploy/isolation/update-client.py /etc/hermes-agent/386gpt-client.yaml "$settings/gateway.key" --runtime crash --url http://100.74.13.43:8644
sudo systemctl daemon-reload
sudo systemctl enable hermes-gateway-386gpt.service
sudo systemctl restart hermes-gateway-386gpt.service
sudo python3 deploy/isolation/smoke.py http://100.74.13.43:8644 "$settings/gateway.key"
echo 'Crash is isolated. Publish the updated client connection to etcd and book14; restart the Go clients.'
