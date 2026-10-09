#!/bin/sh
set -eu
export ADDRESS=${ADDRESS:-127.0.0.1:21351}
export PUBLIC_ORIGIN=${PUBLIC_ORIGIN:-https://386gpt.dev14.portnumber53.com}
export GOOGLE_OAUTH_CLIENT_FILE=${GOOGLE_OAUTH_CLIENT_FILE:-$HOME/.config/386gpt/google-oauth.json}
# The API binds to loopback behind the development HTTPS proxy. OAuth is required when its
# configured client file is present; a missing client fails startup.
export AUTH_MODE=development
exec "${1:-./.air/tmp/386gpt}"
