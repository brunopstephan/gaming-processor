#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Duplicatas SQS: a mesma mensagem 5x (dedup ids diferentes, para passar da deduplicacao FIFO)"
new_wallet 100.00
ext=$(uuid); msg="msg-$ext"
body=$(sqs_envelope "$msg" "$W_ID" "$W_PLAYER" "$ext" BET 25.00)
before=$(prom_sum inbox_duplicates_total)
sent "carteira $W_ID (100.00); mensagem $msg enviada 5x com MessageDeduplicationId distintos; inbox_duplicates_total antes=$before"
expect "1 debito, saldo 75.00; sum(inbox_duplicates_total) aumenta em 4"
for i in 1 2 3 4 5; do "$ROOT/scripts/sqs-send.sh" provider-a "$W_ID" "$body" "$msg-dedup-$i" >/dev/null 2>&1; done
dup_ok() { [ "$(prom_sum inbox_duplicates_total)" -ge $((before+4)) ]; }
poll 60 "inbox_duplicates_total +4 no Prometheus" dup_ok || true
after=$(prom_sum inbox_duplicates_total)
got "inbox_duplicates_total depois=$after (delta $((after-before)))"
check "delta de inbox_duplicates_total" "$((after-before))" 4
check "inbox_messages com o messageId" "$(psql_q "select count(*) from inbox_messages where message_id='$msg'")" 1
check "DEBITs no ledger" "$(psql_q "select count(*) from wallet_ledger_entries l join wager_transactions t on t.id=l.transaction_id where t.external_transaction_id='$ext' and l.direction='DEBIT'")" 1
check "saldo API" "$(wallet_balance "$W_ID")" 75.00
scn_end
