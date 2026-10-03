#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Mesma operacao por HTTP e por SQS ao mesmo tempo: um unico debito"
new_wallet 100.00
ext=$(uuid); msg="msg-$ext"
sent "carteira $W_ID (100.00); BET 25.00 ext $ext via POST HTTP e via SQS (messageId $msg) disparados simultaneamente"
expect "1 debito no ledger, saldo 75.00 (debitado uma vez); inbox_messages com o messageId, processado em ate 30s"
( post_wager "$RUN_DIR/http" "$W_ID" "$W_PLAYER" "$ext" BET 25.00 ) &
( "$ROOT/scripts/sqs-send.sh" provider-a "$W_ID" "$(sqs_envelope "$msg" "$W_ID" "$W_PLAYER" "$ext" BET 25.00)" >/dev/null 2>&1 ) &
wait
inbox_done() { [ "$(psql_q "select count(*) from inbox_messages where message_id='$msg' and processed_at is not null")" = 1 ]; }
poll 30 "inbox_messages processado" inbox_done || true
got "HTTP: $(head -1 "$RUN_DIR/http") $(tail -n +2 "$RUN_DIR/http" | json_field 'd.get("status")') replay=$(tail -n +2 "$RUN_DIR/http" | json_field 'd.get("idempotentReplay")')"
check "inbox processada" "$(psql_q "select count(*) from inbox_messages where message_id='$msg' and processed_at is not null")" 1
check "linhas em wager_transactions" "$(psql_q "select count(*) from wager_transactions where provider_id='provider-a' and external_transaction_id='$ext'")" 1
check "DEBITs no ledger" "$(psql_q "select count(*) from wallet_ledger_entries l join wager_transactions t on t.id=l.transaction_id where t.external_transaction_id='$ext' and l.direction='DEBIT'")" 1
check "saldo API" "$(wallet_balance "$W_ID")" 75.00
check "reconciliacao consistente" "$(reconcile "$W_ID")" True
scn_end
