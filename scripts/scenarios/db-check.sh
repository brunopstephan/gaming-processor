#!/usr/bin/env bash
SCENARIO_NAME=db-check
source "$(dirname "$0")/lib.sh"
scn_begin "Invariantes globais do banco depois de todos os cenarios"
echo "Contagens: carteiras=$(psql_q 'select count(*) from wallets') transacoes=$(psql_q 'select count(*) from wager_transactions') lancamentos=$(psql_q 'select count(*) from wallet_ledger_entries') inbox=$(psql_q 'select count(*) from inbox_messages') outbox=$(psql_q 'select count(*) from outbox_events')"
echo "Transacoes por status: $(psql_q "select status||'='||count(*) from wager_transactions group by status order by 1" | tr '\n' ' ')"
expect "0 saldos negativos; 0 carteiras com balance_minor diferente de creditos-debitos; 0 transacoes com mais de um lancamento"
check "carteiras com saldo negativo" "$(psql_q 'select count(*) from wallets where balance_minor<0')" 0
check "carteiras com saldo != soma do ledger" "$(psql_q "select count(*) from wallets w where w.balance_minor <> coalesce((select sum(case direction when 'CREDIT' then amount_minor else -amount_minor end) from wallet_ledger_entries l where l.wallet_id=w.id),0)")" 0
check "transacoes com mais de um lancamento" "$(psql_q 'select count(*) from (select transaction_id from wallet_ledger_entries group by 1 having count(*)>1) x')" 0
check "chaves de idempotencia duplicadas" "$(psql_q 'select count(*) from (select provider_id,idempotency_key from wager_transactions where idempotency_key is not null group by 1,2 having count(*)>1) x')" 0
scn_end
