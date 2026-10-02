#!/usr/bin/env bash
# Prints an access token for a client of the wallet realm (client_credentials).
# Usage: scripts/token.sh provider-a|provider-b|wallet-internal
set -euo pipefail
client=${1:?client id}
kc=${KEYCLOAK_URL:-http://localhost:8080}
curl -sf "$kc/realms/wallet/protocol/openid-connect/token" \
  -d grant_type=client_credentials -d client_id="$client" -d client_secret="$client-secret" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])'
