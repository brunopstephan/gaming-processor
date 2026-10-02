#!/usr/bin/env bash
# Prints an access token for a client of the wallet realm (client_credentials).
# Usage: scripts/token.sh provider-a|provider-b|wallet-internal
set -euo pipefail
client=${1:?client id}
kc=${KEYCLOAK_URL:-http://localhost:${KEYCLOAK_PORT:-8080}}
if ! body=$(curl -sS --fail-with-body "$kc/realms/wallet/protocol/openid-connect/token" \
  -d grant_type=client_credentials -d client_id="$client" -d client_secret="$client-secret" 2>&1); then
  echo "token: Keycloak at $kc returned an error for client $client: $(echo "$body" | tr '\n' ' ')" >&2
  exit 1
fi
printf '%s' "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])' ||
  { echo "token: unexpected Keycloak response: $body" >&2; exit 1; }
