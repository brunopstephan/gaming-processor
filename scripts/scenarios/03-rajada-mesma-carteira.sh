#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Rajada na mesma carteira: 100 apostas diferentes de 1.00 em paralelo sobre saldo 50.00"
N=100
new_wallet 50.00
d="$RUN_DIR/r"; mkdir -p "$d"
sent "carteira $W_ID (50.00); $N BETs distintas de 1.00 em paralelo"
for i in $(seq 1 $N); do post_wager "$d/$i" "$W_ID" "$W_PLAYER" "$(uuid)" BET 1.00 & done
wait
summarize "$d"
expect "50 PROCESSED, 50 INSUFFICIENT_FUNDS, saldo 0.00, reconciliacao consistente, 51 lancamentos (1 credito de abertura + 50 debitos)"
got "http: $(status_counts "$d")"
check "PROCESSED" "$(count_tsv "$d" 2 PROCESSED)" 50
check "INSUFFICIENT_FUNDS" "$(count_tsv "$d" 2 INSUFFICIENT_FUNDS)" 50
check "saldo API" "$(wallet_balance "$W_ID")" 0.00
check "reconciliacao consistente" "$(reconcile "$W_ID")" True
check "lancamentos no ledger" "$(psql_q "select count(*) from wallet_ledger_entries where wallet_id='$W_ID'")" 51
check "debitos" "$(psql_q "select count(*) from wallet_ledger_entries where wallet_id='$W_ID' and direction='DEBIT'")" 50
scn_end
