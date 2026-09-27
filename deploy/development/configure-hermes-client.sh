#!/bin/sh
set -eu
set +x

host=${HERMES_SSH_HOST:-crash}
destination=${HERMES_CONFIG:-$HOME/.hermes/386gpt.yaml}
umask 077
mkdir -p "$(dirname -- "$destination")"
temporary=$(mktemp "${destination}.XXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM

ssh -o BatchMode=yes "$host" 'sudo -n cat /etc/hermes-agent/386gpt-client.yaml' > "$temporary"
test -s "$temporary"
chmod 600 "$temporary"
mv "$temporary" "$destination"
echo "Installed the private Hermes connection at $destination (credential hidden)."
