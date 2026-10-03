#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Queda de replica durante rajada: 60 apostas de 1.00 enquanto uma replica do wallet e morta e recriada"
N=60
new_wallet 100.00
d="$RUN_DIR/r"; mkdir -p "$d"
exts=(); for i in $(seq 1 $N); do exts+=("$(uuid)"); done
victim=$(docker compose ps -q wallet | head -1)
sent "carteira $W_ID (100.00); $N BETs distintas de 1.00 em segundo plano com atrasos aleatorios; docker kill da replica ${victim:0:12}; docker compose up -d wallet; reenvio das respostas nao definitivas com a mesma Idempotency-Key"
expect "saldo = 100 - (BETs PROCESSED distintas), reconciliacao consistente, sem lancamentos duplicados, 3 replicas saudaveis ao final"
(
  for i in $(seq 1 $N); do
    post_wager "$d/$i" "$W_ID" "$W_PLAYER" "${exts[$((i-1))]}" BET 1.00 &
    sleep "0.0$((RANDOM % 9))"
  done
  wait
) &
burst=$!
sleep 0.4
docker kill "$victim" >/dev/null
sleep 1
docker compose up -d --no-deps wallet >/dev/null 2>&1  # --no-deps: sem isso o ministack_init roda de novo e rotaciona as chaves SQS das outras replicas
wait "$burst"
summarize "$d"
first="$(status_counts "$d")"
echo "Primeira passada: $first"
# reenvia o que nao teve resposta definitiva (200 ou 4xx) com a mesma chave
retried=0
for i in $(seq 1 $N); do
  st=$(head -1 "$d/$i")
  case $st in 2??|4??) continue ;; esac
  retried=$((retried+1))
  for try in $(seq 1 60); do
    post_wager "$d/$i" "$W_ID" "$W_PLAYER" "${exts[$((i-1))]}" BET 1.00
    st=$(head -1 "$d/$i"); case $st in 2??|4??) break ;; esac
    sleep 1
  done
done
summarize "$d"
processed=$(count_tsv "$d" 2 PROCESSED)
got "reenviadas: $retried; apos reenvio: $(status_counts "$d")"
check "respostas definitivas (200)" "$(count_tsv "$d" 1 200)" $N
check "PROCESSED" "$processed" $N
check "saldo API" "$(wallet_balance "$W_ID")" "$(python3 -c "print(f'{100-$processed:.2f}')")"
check "reconciliacao consistente" "$(reconcile "$W_ID")" True
check "debitos no ledger" "$(psql_q "select count(*) from wallet_ledger_entries where wallet_id='$W_ID' and direction='DEBIT'")" "$processed"
check "transacoes duplicadas (mesmo ext)" "$(psql_q "select count(*) from (select external_transaction_id from wager_transactions where wallet_id='$W_ID' and kind='BET' group by 1 having count(*)>1) x")" 0
check "transacoes com mais de um lancamento" "$(psql_q "select count(*) from (select transaction_id from wallet_ledger_entries where wallet_id='$W_ID' group by 1 having count(*)>1) x")" 0
wait_replicas_healthy 3 180 || FAILS=$((FAILS+1))
check "replicas saudaveis" "$(docker compose ps wallet --format '{{.Status}}' | grep -c '(healthy)' || true)" 3
scn_end
