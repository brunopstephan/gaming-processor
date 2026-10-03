#!/usr/bin/env bash
# Helpers compartilhados pelos cenarios manuais. Uso: source "$(dirname "$0")/lib.sh"
# Requer: stack do compose no ar, curl e python3.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
BASE=${WALLET_URL:-http://localhost:${WALLET_PORT:-8000}}
PROM=${PROMETHEUS_URL:-http://localhost:${PROMETHEUS_PORT:-9090}}
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/scenario.XXXXXX")
trap 'rm -rf "$RUN_DIR"' EXIT
FAILS=0
SCENARIO_NAME=${SCENARIO_NAME:-$(basename "$0" .sh)}

uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }
now_s() { python3 -c 'import time; print(f"{time.time():.2f}")'; }

# token CLIENT: token em cache por execucao.
token() {
  local f="$RUN_DIR/token-$1"
  # escrita atomica: jobs paralelos nunca leem arquivo pela metade
  [ -s "$f" ] || { "$ROOT/scripts/token.sh" "$1" >"$f.$$" && mv "$f.$$" "$f"; }
  cat "$f"
}

# json_field 'EXPR' < json : imprime EXPR avaliada sobre d (vazio se falhar).
json_field() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)" 2>/dev/null || true; }

# http METHOD URL CLIENT [BODY] [IDEMPOTENCY_KEY]: define HTTP_STATUS e HTTP_BODY (status 000 se a conexao falhar).
http() {
  local out
  local args=(-sS -m 60 -w '\n%{http_code}' -X "$1" "$2" -H "Authorization: Bearer $(token "$3")")
  [ -n "${4:-}" ] && args+=(-H 'Content-Type: application/json' -d "$4")
  [ -n "${5:-}" ] && args+=(-H "Idempotency-Key: $5")
  out=$(curl "${args[@]}" 2>&1) || out=$'\n000'
  HTTP_STATUS=${out##*$'\n'}
  HTTP_BODY=${out%$'\n'*}
}

# http_to_file OUTFILE METHOD URL CLIENT [BODY] [KEY]: igual a http, mas grava "status\nbody" (para jobs paralelos).
http_to_file() {
  local out=$1; shift
  http "$@"
  printf '%s\n%s\n' "$HTTP_STATUS" "$HTTP_BODY" >"$out.tmp"
  mv "$out.tmp" "$out"
}

# psql_q "SQL": uma linha por registro, campos separados por |.
psql_q() {
  docker compose exec -T postgres psql -U wallet_owner -d wallet -At -v ON_ERROR_STOP=1 -c "$1"
}

# new_wallet AMOUNT: cria carteira; define W_ID e W_PLAYER.
new_wallet() {
  token provider-a >/dev/null; token provider-b >/dev/null  # aquece o cache antes de qualquer job paralelo
  W_PLAYER=$(uuid)
  http POST "$BASE/wallets" wallet-internal \
    "{\"playerId\":\"$W_PLAYER\",\"initialBalance\":{\"amount\":\"$1\",\"currency\":\"BRL\"}}"
  [ "$HTTP_STATUS" = 201 ] || { echo "falha ao criar carteira: $HTTP_STATUS $HTTP_BODY" >&2; exit 2; }
  W_ID=$(printf '%s' "$HTTP_BODY" | json_field 'd["id"]')
}

# wager_json WALLET PLAYER EXT KIND AMOUNT [REF_EXT] [PROVIDER] [ROUND]
wager_json() {
  local ref=""
  [ -n "${6:-}" ] && ref=",\"referenceExternalTransactionId\":\"$6\""
  printf '{"providerId":"%s","externalTransactionId":"%s","playerId":"%s","walletId":"%s","roundId":"%s","gameId":"g1","kind":"%s","money":{"amount":"%s","currency":"BRL"}%s}' \
    "${7:-provider-a}" "$3" "$2" "$1" "${8:-r1}" "$4" "$5" "$ref"
}

# post_wager FILE WALLET PLAYER EXT KIND AMOUNT [REF]: POST com Idempotency-Key provider-a:EXT, resultado em FILE.
post_wager() {
  http_to_file "$1" POST "$BASE/wagering/transactions" provider-a \
    "$(wager_json "$2" "$3" "$4" "$5" "$6" "${7:-}")" "provider-a:$4"
}

# wallet_balance WALLET: saldo (ex.: 75.00) via API.
wallet_balance() {
  http GET "$BASE/wallets/$1" wallet-internal
  printf '%s' "$HTTP_BODY" | json_field 'd["balance"]["amount"]'
}

# reconcile WALLET: imprime True/False.
reconcile() {
  http POST "$BASE/wallets/$1/reconciliation" wallet-internal
  printf '%s' "$HTTP_BODY" | json_field 'd["consistent"]'
}

# summarize DIR: gera DIR.tsv com uma linha por resposta: http, status|failureCode, replay, transactionId, saldo.
summarize() {
  python3 - "$1" <<'PY'
import json, os, sys
d = sys.argv[1]
rows = []
for name in sorted(os.listdir(d)):
    lines = open(os.path.join(d, name)).read().split("\n", 1)
    st, body = lines[0], (lines[1] if len(lines) > 1 else "")
    try:
        j = json.loads(body)
    except Exception:
        j = {}
    if "error" in j and isinstance(j["error"], dict):
        tag = j["error"].get("failureCode", "")
    else:
        tag = j.get("failureCode") or j.get("status", "")
    bal = (j.get("balance") or {}).get("amount", "")
    rows.append("\t".join([st, str(tag), str(j.get("idempotentReplay", "")), str(j.get("transactionId", "")), bal]))
open(d + ".tsv", "w").write("\n".join(rows) + "\n")
PY
}
# count_tsv DIR COL VALUE: quantas linhas de DIR.tsv tem a coluna COL (1-based) igual a VALUE.
count_tsv() { awk -F'\t' -v c="$2" -v v="$3" '$c==v{n++} END{print n+0}' "$1.tsv"; }
# status_counts DIR: "200 PROCESSED: 50" ...
status_counts() { cut -f1,2 "$1.tsv" | sort | uniq -c | awk '{printf "%s%s=%s", (NR>1?", ":""), $2 "/" $3, $1}'; }

# ---------- relato ----------
scn_begin() { echo "=== $SCENARIO_NAME ==="; echo "Finalidade: $1"; SCN_START=$(date +%s); }
sent() { echo "Enviou: $*"; }
expect() { echo "Esperado: $*"; }
got() { echo "Obtido: $*"; }
# check "descricao" ATUAL ESPERADO
check() {
  if [ "$2" = "$3" ]; then echo "  [ok] $1: $2"
  else echo "  [FALHOU] $1: obtido '$2', esperado '$3'"; FAILS=$((FAILS+1)); fi
}
scn_end() {
  echo "Duracao: $(( $(date +%s) - SCN_START ))s"
  if [ "$FAILS" -eq 0 ]; then echo "RESULTADO: OK"; else echo "RESULTADO: FALHOU ($FAILS verificacao(oes))"; exit 1; fi
}

# poll TIMEOUT_S DESCRICAO CMD...: repete CMD ate sair com 0.
poll() {
  local t=$1 desc=$2; shift 2
  local end=$(( $(date +%s) + t ))
  while ! "$@" >/dev/null 2>&1; do
    [ "$(date +%s)" -lt "$end" ] || { echo "  timeout ($t s) esperando: $desc"; return 1; }
    sleep 1
  done
}

# prom_sum METRIC: soma da metrica nas replicas (0 se nao existir).
prom_sum() {
  curl -sS -m 10 --get "$PROM/api/v1/query" --data-urlencode "query=sum($1)" |
    python3 -c 'import json,sys
r=json.load(sys.stdin)["data"]["result"]
print(int(float(r[0]["value"][1])) if r else 0)'
}

# wait_replicas_healthy [N] [TIMEOUT_S]
wait_replicas_healthy() {
  local n=${1:-3} t=${2:-180}
  local end=$(( $(date +%s) + t )) stable=0
  while :; do
    local c; c=$(docker compose ps wallet --format '{{.Status}}' | grep -c '(healthy)' || true)
    if [ "$c" -ge "$n" ] && [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/health/ready" || true)" = 200 ]; then
      stable=$((stable+1)); [ "$stable" -ge 4 ] && return 0   # saude estavel por ~8s (o healthcheck atrasa em relacao ao estado real)
    else stable=0; fi
    [ "$(date +%s)" -lt "$end" ] || { echo "  timeout esperando $n replicas saudaveis (ha $c)"; return 1; }
    sleep 2
  done
}

# sqs_envelope MSGID WALLET PLAYER EXT KIND AMOUNT: corpo JSON do envelope WagerTransactionRequested.
sqs_envelope() {
  printf '{"messageId":"%s","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z","data":{"providerId":"provider-a","externalTransactionId":"%s","idempotencyKey":"provider-a:%s","playerId":"%s","walletId":"%s","roundId":"r1","gameId":"g1","kind":"%s","money":{"amount":"%s","currency":"BRL"}}}' \
    "$1" "$4" "$4" "$3" "$2" "$5" "$6"
}
