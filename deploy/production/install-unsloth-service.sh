#!/bin/sh
set -eu
set +x

test "$(id -u)" -eq 0 || { echo "this script must run as root" >&2; exit 1; }
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test -x /home/grimlock/.unsloth/studio/unsloth_studio/bin/unsloth
install -d -m 0755 /usr/local/lib/386gpt
install -m 0644 "$script_dir/load-unsloth-model.py" /usr/local/lib/386gpt/load-unsloth-model.py
install -m 0644 "$script_dir/unsloth.service" /etc/systemd/system/unsloth.service

# After= includes Unsloth's ExecStartPost model-load readiness check. Wants=
# lets Hermes remain available while Unsloth recovers from a later restart.
install -d -m 0755 /etc/systemd/system/hermes-gateway-386gpt.service.d
printf '%s\n' '[Unit]' 'Wants=unsloth.service' 'After=unsloth.service' \
    > /etc/systemd/system/hermes-gateway-386gpt.service.d/unsloth.conf
systemd-analyze verify /etc/systemd/system/unsloth.service
systemctl daemon-reload
systemctl enable unsloth.service
systemctl restart unsloth.service
echo "Unsloth is enabled at boot and Gemma is loaded."
