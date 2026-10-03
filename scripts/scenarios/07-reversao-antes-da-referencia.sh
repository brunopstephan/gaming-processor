#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Reversao que chega antes da referencia: ROLLBACK fica PENDING_REFERENCE e e aplicado quando a BET chega"
new_wallet 100.00
bet=$(uuid); rb=$(uuid)
sent "carteira $W_ID (100.00); 1) ROLLBACK 30.00 (ext $rb) referenciando a BET $bet, ainda nao enviada; 2) depois a BET 30.00"
expect "ROLLBACK -> 202 PENDING_REFERENCE; BET -> 200; em ate 60s o ROLLBACK vira PROCESSED; saldo final 100.00"
post_wager "$RUN_DIR/rb" "$W_ID" "$W_PLAYER" "$rb" ROLLBACK 30.00 "$bet"
check "ROLLBACK antecipado: HTTP" "$(head -1 "$RUN_DIR/rb")" 202
check "ROLLBACK antecipado: status" "$(tail -n +2 "$RUN_DIR/rb" | json_field 'd.get("status")')" PENDING_REFERENCE
check "saldo antes da BET" "$(wallet_balance "$W_ID")" 100.00
post_wager "$RUN_DIR/bet" "$W_ID" "$W_PLAYER" "$bet" BET 30.00
check "BET: HTTP" "$(head -1 "$RUN_DIR/bet")" 200
rb_done() {
  http GET "$BASE/providers/provider-a/wagering/transactions/$rb" provider-a
  [ "$(printf '%s' "$HTTP_BODY" | json_field 'd.get("status")')" = PROCESSED ]
}
poll 60 "ROLLBACK PROCESSED" rb_done || true
http GET "$BASE/providers/provider-a/wagering/transactions/$rb" provider-a
got "ROLLBACK pela rota do provedor: $HTTP_STATUS $(printf '%s' "$HTTP_BODY" | json_field 'd.get("status")')"
check "ROLLBACK status final" "$(printf '%s' "$HTTP_BODY" | json_field 'd.get("status")')" PROCESSED
check "saldo final API" "$(wallet_balance "$W_ID")" 100.00
check "lancamentos (abertura, debito, credito)" "$(psql_q "select string_agg(direction,',' order by created_at,direction) from wallet_ledger_entries where wallet_id='$W_ID'" | tr ',' '\n' | sort | tr '\n' ',')" "CREDIT,CREDIT,DEBIT,"
check "reconciliacao consistente" "$(reconcile "$W_ID")" True
scn_end
