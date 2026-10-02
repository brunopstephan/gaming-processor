BEGIN;

CREATE TABLE wallets (
    id            uuid        PRIMARY KEY,
    player_id     uuid        NOT NULL,
    currency      char(3)     NOT NULL CONSTRAINT wallets_currency_check CHECK (currency ~ '^[A-Z]{3}$'),
    balance_minor bigint      NOT NULL CONSTRAINT wallets_balance_minor_check CHECK (balance_minor >= 0),
    version       bigint      NOT NULL CONSTRAINT wallets_version_check CHECK (version >= 1),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT wallets_player_currency_key UNIQUE (player_id, currency)
);

GRANT SELECT, INSERT, UPDATE ON wallets TO wallet_app;

COMMIT;
