#!/bin/sh
# Initialize a new named volume from an explicitly exported data-only snapshot.
set -eu
snapshot=${1:?usage: install-state.sh SNAPSHOT_DIRECTORY VOLUME_NAME}
volume=${2:?usage: install-state.sh SNAPSHOT_DIRECTORY VOLUME_NAME}
case "$volume" in 386gpt-*_state) ;; *) echo 'Unexpected state volume name' >&2; exit 1;; esac
docker volume create "$volume" >/dev/null
docker run --rm --network none --user 0:0 --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --security-opt no-new-privileges:true \
  --entrypoint /bin/sh -v "$snapshot:/snapshot:ro" -v "$volume:/state" 386gpt-hermes:isolated -ec \
  'test ! -e /state/profile/config.yaml; test ! -e /state/profile/state.db; cp -R /snapshot/. /state/; chmod 700 /state; chown -R 10001:10001 /state'
