#!/bin/sh
set -eu
set +x
: "${ACCESS_ISSUER:?required}" "${ACCESS_AUDIENCE:?required}" "${ACCESS_SERVICE_CLIENT_ID:?required}"
exec sh ../deploy/jenkins/with-cloudflare-env.sh npx wrangler deploy \
 --var "ACCESS_ISSUER:$ACCESS_ISSUER" \
 --var "ACCESS_AUDIENCE:$ACCESS_AUDIENCE" \
 --var "ACCESS_SERVICE_CLIENT_ID:$ACCESS_SERVICE_CLIENT_ID"
