#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
scn_begin "Carteiras paralelas: 20 carteiras x 10 apostas de 1.00, as 200 requisicoes ao mesmo tempo"
W=20; B=10
ids=(); players=()
for w in $(seq 1 $W); do new_wallet 50.00; ids+=("$W_ID"); players+=("$W_PLAYER"); done
d="$RUN_DIR/r"; mkdir -p "$d"
sent "$W carteiras (50.00); $B BETs distintas de 1.00 em cada; $((W*B)) POSTs simultaneos"
t0=$(now_s)
for w in $(seq 0 $((W-1))); do for b in $(seq 1 $B); do post_wager "$d/$w-$b" "${ids[$w]}" "${players[$w]}" "$(uuid)" BET 1.00 & done; done
wait
t1=$(now_s)
summarize "$d"
expect "200 PROCESSED no total; cada carteira com saldo 40.00"
got "http: $(status_counts "$d"); tempo total $(python3 -c "print(round($t1-$t0,2))")s"
check "PROCESSED" "$(count_tsv "$d" 2 PROCESSED)" $((W*B))
bad=0
for w in $(seq 0 $((W-1))); do [ "$(wallet_balance "${ids[$w]}")" = 40.00 ] || bad=$((bad+1)); done
check "carteiras com saldo diferente de 40.00" "$bad" 0
check "carteiras inconsistentes na reconciliacao" "$(for id in "${ids[@]}"; do reconcile "$id"; done | grep -vc True || true)" 0
echo "Tempo total das $((W*B)) requisicoes paralelas: $(python3 -c "print(round($t1-$t0,2))")s"
scn_end
