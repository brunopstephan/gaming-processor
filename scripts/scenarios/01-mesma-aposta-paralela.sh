#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Mesma aposta (mesma Idempotency-Key e corpo) 50x em paralelo via nginx: um unico efeito"
N=50
new_wallet 100.00
ext=$(uuid); d="$RUN_DIR/r"; mkdir -p "$d"
sent "carteira $W_ID (100.00); $N POSTs identicos de BET 25.00 (ext $ext) em paralelo"
for i in $(seq 1 $N); do post_wager "$d/$i" "$W_ID" "$W_PLAYER" "$ext" BET 25.00 & done
wait
summarize "$d"
expect "1 resposta idempotentReplay=false, 49 true, todas 200, mesmo transactionId, saldo 75.00; 1 linha em wager_transactions e 1 DEBIT"
got "http: $(status_counts "$d")"
check "respostas 200" "$(count_tsv "$d" 1 200)" $N
check "replay=false" "$(count_tsv "$d" 3 False)" 1
check "replay=true" "$(count_tsv "$d" 3 True)" $((N-1))
check "transactionIds distintos" "$(cut -f4 "$d.tsv" | sort -u | wc -l | tr -d ' ')" 1
check "saldo API" "$(wallet_balance "$W_ID")" 75.00
check "linhas em wager_transactions" "$(psql_q "select count(*) from wager_transactions where idempotency_key='provider-a:$ext'")" 1
check "DEBITs no ledger" "$(psql_q "select count(*) from wallet_ledger_entries l join wager_transactions t on t.id=l.transaction_id where t.idempotency_key='provider-a:$ext' and l.direction='DEBIT'")" 1
scn_end
