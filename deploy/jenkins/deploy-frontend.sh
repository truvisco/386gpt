#!/bin/sh
set -eu
set +x
exec sh ../deploy/jenkins/with-cloudflare-env.sh npx wrangler deploy
