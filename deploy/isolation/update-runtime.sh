#!/bin/sh
# Update code/images only; preserve every account's credentials, state and workspace.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
hermes_candidate=386gpt-hermes:deploy-$$
boundary_candidate=386gpt-boundary:deploy-$$
trap 'docker image rm "$hermes_candidate" "$boundary_candidate" >/dev/null 2>&1 || true' EXIT
docker build -t "$hermes_candidate" -f deploy/isolation/Dockerfile .
docker build -t "$boundary_candidate" -f deploy/isolation/Dockerfile.proxy .
docker run --rm --network none --entrypoint /opt/venv/bin/python \
    -v "$root/deploy/hermes-gateway:/tests:ro" \
    "$hermes_candidate" /tests/test_channel.py
docker run --rm --network none --entrypoint /opt/venv/bin/python \
    -v "$root/deploy/hermes-gateway:/tests:ro" \
    "$hermes_candidate" /tests/test_github_auth.py
docker tag "$hermes_candidate" 386gpt-hermes:isolated
docker tag "$boundary_candidate" 386gpt-boundary:isolated

# The broker's systemd unit and future logins use this persistent code location.
installed=/opt/386gpt-isolation
if [ "$root" != "$installed" ]; then
    sudo -n mkdir -p "$installed/deploy/hermes-gateway" "$installed/deploy/production"
    sudo -n cp -R deploy/isolation "$installed/deploy/"
    sudo -n cp -R deploy/hermes-gateway/386gpt-channel "$installed/deploy/hermes-gateway/"
    sudo -n cp deploy/production/patch-hermes-runtime.py "$installed/deploy/production/"
fi
sudo -n /home/grimlock/.hermes/386gpt-runtime/venv/bin/python - "$installed" <<'PY'
import importlib.util
from pathlib import Path
import sys
root = Path(sys.argv[1])
sys.path.insert(0, str(root/'deploy/isolation'))
spec = importlib.util.spec_from_file_location('broker', root/'deploy/isolation/tenant-broker.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
settings = Path('/etc/386gpt-isolation')
env = dict(line.split('=', 1) for line in (settings/'compose.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
broker = module.Broker('/var/lib/386gpt-accounts', settings, root/'deploy/isolation/compose.yaml', env['GATEWAY_BIND'], env['UNSLOTH_UPSTREAM'])
broker.ensure_owner()
count = 1
for account in sorted(broker.root.iterdir()):
    if module.ACCOUNT.fullmatch(account.name) and (account/'connection.json').is_file():
        broker.ensure(account.name)
        count += 1
print(f'Updated {count} persistent account stacks; all report healthy.')
PY
sudo -n systemctl restart 386gpt-accounts.service
