#!/usr/bin/env bash
# End-to-end smoke check of the running compose stack, through nginx:
# opens a wallet, bets over HTTP, bets over SQS, then reconciles.
set -euo pipefail
cd "$(dirname "$0")/.."
base=${WALLET_URL:-http://localhost:${WALLET_PORT:-8000}}
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)" 2>/dev/null; }
die() { echo "smoke: $*" >&2; exit 1; }
# call METHOD URL [curl args...]: prints the body; on failure prints status and body to stderr and returns 1.
call() {
  local out status
  out=$(curl -sS -w '\n%{http_code}' "${@:2}" -X "$1" 2>&1) || { echo "$out" >&2; return 1; }
  status=${out##*$'\n'}
  out=${out%$'\n'*}
  case $status in 2??) printf '%s' "$out" ;; *) echo "HTTP $status: $out" >&2; return 1 ;; esac
}
uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }

internal=$(scripts/token.sh wallet-internal)
provider=$(scripts/token.sh provider-a)
player=$(uuid)

wallet=$(call POST "$base/wallets" -H "Authorization: Bearer $internal" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$player\",\"initialBalance\":{\"amount\":\"100.00\",\"currency\":\"BRL\"}}" | json 'd["id"]') || die "open wallet failed"
echo "wallet $wallet"

ext=$(uuid)
bet=$(curl -sS -w '\n%{http_code}' -X POST "$base/wagering/transactions" \
  -H "Authorization: Bearer $provider" -H 'Content-Type: application/json' -H "Idempotency-Key: provider-a:$ext" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}")
status=${bet##*$'\n'}
[ "$status" = 200 ] || die "HTTP bet returned $status: ${bet%$'\n'*}"

ext2=$(uuid)
scripts/sqs-send.sh provider-a "$wallet" "{\"messageId\":\"msg-$ext2\",\"type\":\"WagerTransactionRequested\",\"occurredAt\":\"2026-09-08T12:00:00.000Z\",\"data\":{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext2\",\"idempotencyKey\":\"provider-a:$ext2\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"10.00\",\"currency\":\"BRL\"}}}" >/dev/null

for _ in $(seq 1 30); do
  balance=$(call GET "$base/wallets/$wallet" -H "Authorization: Bearer $internal" 2>/dev/null | json 'd["balance"]["amount"]' 2>/dev/null) || balance=unavailable
  [ "$balance" = "65.00" ] && break
  sleep 1
done
[ "$balance" = "65.00" ] || die "balance $balance, want 65.00"

consistent=$(call POST "$base/wallets/$wallet/reconciliation" -H "Authorization: Bearer $internal" | json 'd["consistent"]') || die "reconciliation failed"
[ "$consistent" = "True" ] || die "reconciliation not consistent"
echo "smoke ok: balance $balance, reconciliation consistent"
