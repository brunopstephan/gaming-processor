BEGIN;

DROP TABLE wallet_ledger_entries;
DROP FUNCTION wallet_ledger_entries_forbid_change();

COMMIT;
