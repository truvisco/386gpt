#!/bin/sh
set -eu
echo 'Host Hermes execution is retired. Follow deploy/isolation/README.md.' >&2
exit 1
test "$(id -u)" -eq 0 || { echo 'run as root' >&2; exit 1; }
runtime=/home/grimlock/.hermes/386gpt-runtime
test -x "$runtime/venv/bin/python"
test -f "$runtime/gateway/platforms/api_server.py"
backup=/home/grimlock/.hermes/profiles/unsloth/state.before-runs.db
if [ ! -f "$backup" ]; then
    sudo -u grimlock "$runtime/venv/bin/python" - "$backup" <<'PY'
import sqlite3, sys
source = sqlite3.connect('file:/home/grimlock/.hermes/profiles/unsloth/state.db?mode=ro', uri=True)
with sqlite3.connect(sys.argv[1]) as dest:
    source.backup(dest)
PY
fi
install -d -m 0755 /etc/systemd/system/hermes-gateway-386gpt.service.d
cat > /etc/systemd/system/hermes-gateway-386gpt.service.d/runtime.conf <<EOF
[Service]
WorkingDirectory=$runtime
Environment=PYTHONPATH=$runtime
ExecStart=
ExecStart=$runtime/venv/bin/python -m hermes_cli.main -p unsloth gateway run
EOF
systemctl daemon-reload
systemctl restart hermes-gateway-386gpt.service
systemctl is-active hermes-gateway-386gpt.service
