#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Disputa: duas apostas diferentes de 80.00 em carteira de 100.00, simultaneas, 10 rodadas; so uma pode passar"
ROUNDS=10
sent "$ROUNDS carteiras (100.00); em cada uma, 2 BETs distintas de 80.00 disparadas ao mesmo tempo"
expect "por rodada: um 200 PROCESSED e um 422 INSUFFICIENT_FUNDS; saldo 20.00; balance_minor 2000, nunca < 0"
for r in $(seq 1 $ROUNDS); do
  new_wallet 100.00
  d="$RUN_DIR/r$r"; mkdir -p "$d"
  for i in 1 2; do post_wager "$d/$i" "$W_ID" "$W_PLAYER" "$(uuid)" BET 80.00 & done
  wait
  summarize "$d"
  tags=$(cut -f1,2 "$d.tsv" | sort | tr '\t\n' '/ ')
  echo "  rodada $r: $tags"
  check "rodada $r 200/PROCESSED" "$(count_tsv "$d" 2 PROCESSED)" 1
  check "rodada $r 422/INSUFFICIENT_FUNDS" "$(count_tsv "$d" 2 INSUFFICIENT_FUNDS)" 1
  check "rodada $r saldo API" "$(wallet_balance "$W_ID")" 20.00
  check "rodada $r balance_minor" "$(psql_q "select balance_minor from wallets where id='$W_ID'")" 2000
done
check "carteiras com saldo negativo" "$(psql_q "select count(*) from wallets where balance_minor<0")" 0
scn_end
