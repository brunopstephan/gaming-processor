BEGIN;

CREATE TABLE wallet_ledger_entries (
    id                   uuid        PRIMARY KEY,
    wallet_id            uuid        NOT NULL REFERENCES wallets (id),
    transaction_id       uuid        NOT NULL,
    direction            text        NOT NULL CONSTRAINT wallet_ledger_entries_direction_check
                                     CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount_minor         bigint      NOT NULL CONSTRAINT wallet_ledger_entries_amount_minor_check
                                     CHECK (amount_minor > 0),
    currency             char(3)     NOT NULL CONSTRAINT wallet_ledger_entries_currency_check
                                     CHECK (currency ~ '^[A-Z]{3}$'),
    balance_before_minor bigint      NOT NULL CONSTRAINT wallet_ledger_entries_balance_before_minor_check
                                     CHECK (balance_before_minor >= 0),
    balance_after_minor  bigint      NOT NULL CONSTRAINT wallet_ledger_entries_balance_after_minor_check
                                     CHECK (balance_after_minor >= 0),
    created_at           timestamptz NOT NULL,
    CONSTRAINT wallet_ledger_entries_balance_math CHECK (
        (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + amount_minor)
        OR (direction = 'DEBIT' AND balance_after_minor = balance_before_minor - amount_minor)
    ),
    -- The entry's wallet must be the wallet of its transaction.
    CONSTRAINT wallet_ledger_entries_transaction_wallet_fkey
        FOREIGN KEY (transaction_id, wallet_id) REFERENCES wager_transactions (id, wallet_id),
    CONSTRAINT wallet_ledger_entries_wallet_transaction_key UNIQUE (wallet_id, transaction_id)
);

-- Stable cursor pagination per wallet (ids are UUIDv7).
CREATE INDEX wallet_ledger_entries_wallet_id_id ON wallet_ledger_entries (wallet_id, id);

CREATE FUNCTION wallet_ledger_entries_forbid_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries is append-only: % is not allowed', TG_OP;
END;
$$;

CREATE TRIGGER wallet_ledger_entries_no_update_delete
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION wallet_ledger_entries_forbid_change();

CREATE TRIGGER wallet_ledger_entries_no_truncate
    BEFORE TRUNCATE ON wallet_ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION wallet_ledger_entries_forbid_change();

GRANT SELECT, INSERT ON wallet_ledger_entries TO wallet_app;

COMMIT;
