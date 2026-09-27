#!/bin/sh
set -eu
export ADDRESS=${ADDRESS:-127.0.0.1:8080}
export PUBLIC_ORIGIN=${PUBLIC_ORIGIN:-http://localhost:5173}
export GOOGLE_OAUTH_CLIENT_FILE=${GOOGLE_OAUTH_CLIENT_FILE:-$HOME/.config/386gpt/google-oauth.json}
# This only permits loopback origins. Google OAuth remains required when its
# configured client file is present; a missing client fails startup.
export AUTH_MODE=development
exec "${1:-./.air/tmp/386gpt}"
