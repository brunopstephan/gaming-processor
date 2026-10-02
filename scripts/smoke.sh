#!/usr/bin/env bash
# End-to-end smoke check of the running compose stack, through nginx:
# opens a wallet, bets over HTTP, bets over SQS, then reconciles.
set -euo pipefail
cd "$(dirname "$0")/.."
base=${WALLET_URL:-http://localhost:8000}
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }

internal=$(scripts/token.sh wallet-internal)
provider=$(scripts/token.sh provider-a)
player=$(uuid)

wallet=$(curl -sf -X POST "$base/wallets" -H "Authorization: Bearer $internal" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$player\",\"initialBalance\":{\"amount\":\"100.00\",\"currency\":\"BRL\"}}" | json 'd["id"]')
echo "wallet $wallet"

ext=$(uuid)
status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$base/wagering/transactions" \
  -H "Authorization: Bearer $provider" -H 'Content-Type: application/json' -H "Idempotency-Key: provider-a:$ext" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}")
[ "$status" = 200 ] || { echo "HTTP bet returned $status"; exit 1; }

ext2=$(uuid)
scripts/sqs-send.sh provider-a "$wallet" "{\"messageId\":\"msg-$ext2\",\"type\":\"WagerTransactionRequested\",\"occurredAt\":\"2026-09-08T12:00:00.000Z\",\"data\":{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext2\",\"idempotencyKey\":\"provider-a:$ext2\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"10.00\",\"currency\":\"BRL\"}}}" >/dev/null

for _ in $(seq 1 30); do
  balance=$(curl -sf "$base/wallets/$wallet" -H "Authorization: Bearer $internal" | json 'd["balance"]["amount"]')
  [ "$balance" = "65.00" ] && break
  sleep 1
done
[ "$balance" = "65.00" ] || { echo "balance $balance, want 65.00"; exit 1; }

consistent=$(curl -sf -X POST "$base/wallets/$wallet/reconciliation" -H "Authorization: Bearer $internal" | json 'd["consistent"]')
[ "$consistent" = "True" ] || { echo "reconciliation not consistent"; exit 1; }
echo "smoke ok: balance $balance, reconciliation consistent"
