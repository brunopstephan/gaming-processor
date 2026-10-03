#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Conflitos de idempotencia: mesma chave com corpo diferente e mesmo externalTransactionId com outra chave"
new_wallet 100.00
ext=$(uuid)
sent "carteira $W_ID (100.00); BET 25.00 ext $ext (chave provider-a:$ext); depois (a) mesma chave com 30.00; (b) mesmo ext com outra chave"
expect "BET original 200 (saldo 75.00); (a) 409 IDEMPOTENCY_KEY_CONFLICT; (b) 409 EXTERNAL_TRANSACTION_CONFLICT; saldo segue 75.00"
post_wager "$RUN_DIR/a" "$W_ID" "$W_PLAYER" "$ext" BET 25.00
check "BET original HTTP" "$(head -1 "$RUN_DIR/a")" 200
http POST "$BASE/wagering/transactions" provider-a "$(wager_json "$W_ID" "$W_PLAYER" "$ext" BET 30.00)" "provider-a:$ext"
got "(a) $HTTP_STATUS $HTTP_BODY"
check "(a) HTTP" "$HTTP_STATUS" 409
check "(a) failureCode" "$(printf '%s' "$HTTP_BODY" | json_field 'd["error"]["failureCode"]')" IDEMPOTENCY_KEY_CONFLICT
http POST "$BASE/wagering/transactions" provider-a "$(wager_json "$W_ID" "$W_PLAYER" "$ext" BET 25.00)" "provider-a:outra-$ext"
got "(b) $HTTP_STATUS $HTTP_BODY"
check "(b) HTTP" "$HTTP_STATUS" 409
check "(b) failureCode" "$(printf '%s' "$HTTP_BODY" | json_field 'd["error"]["failureCode"]')" EXTERNAL_TRANSACTION_CONFLICT
check "saldo API" "$(wallet_balance "$W_ID")" 75.00
check "linhas para o ext" "$(psql_q "select count(*) from wager_transactions where provider_id='provider-a' and external_transaction_id='$ext'")" 1
check "lancamentos do ext" "$(psql_q "select count(*) from wallet_ledger_entries l join wager_transactions t on t.id=l.transaction_id where t.external_transaction_id='$ext'")" 1
scn_end
