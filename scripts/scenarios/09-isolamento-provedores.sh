#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Isolamento entre provedores: provider-b nao enxerga nem opera em nome do provider-a"
new_wallet 100.00
ext=$(uuid); ext2=$(uuid)
sent "carteira $W_ID (100.00); BET 25.00 do provider-a (ext $ext); provider-b: GET por id, GET pela rota do provider-a, POST com providerId provider-a (ext $ext2)"
expect "GET por id 404; GET rota do provider-a 403; POST 403; sem linha no banco para $ext2; saldo 75.00"
post_wager "$RUN_DIR/a" "$W_ID" "$W_PLAYER" "$ext" BET 25.00
check "BET do provider-a" "$(head -1 "$RUN_DIR/a")" 200
txid=$(tail -n +2 "$RUN_DIR/a" | json_field 'd["transactionId"]')
http GET "$BASE/wagering/transactions/$txid" provider-b
check "provider-b GET por id" "$HTTP_STATUS" 404
http GET "$BASE/providers/provider-a/wagering/transactions/$ext" provider-b
check "provider-b GET rota do provider-a" "$HTTP_STATUS" 403
http POST "$BASE/wagering/transactions" provider-b "$(wager_json "$W_ID" "$W_PLAYER" "$ext2" BET 10.00)" "provider-a:$ext2"
check "provider-b POST com providerId provider-a" "$HTTP_STATUS" 403
check "linhas para o ext do POST negado" "$(psql_q "select count(*) from wager_transactions where external_transaction_id='$ext2'")" 0
check "saldo API" "$(wallet_balance "$W_ID")" 75.00
scn_end
