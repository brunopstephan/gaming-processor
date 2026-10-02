BEGIN;

CREATE TABLE wager_transactions (
    id                                uuid         PRIMARY KEY,
    origin                            text         NOT NULL CONSTRAINT wager_transactions_origin_check
                                                   CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    kind                              text         NOT NULL CONSTRAINT wager_transactions_kind_check
                                                   CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    status                            text         NOT NULL CONSTRAINT wager_transactions_status_check
                                                   CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    -- No FK: a WALLET_NOT_FOUND rejection keeps the requested wallet id for audit.
    wallet_id                         uuid         NOT NULL,
    player_id                         uuid         NOT NULL,
    amount_minor                      bigint       NOT NULL CONSTRAINT wager_transactions_amount_minor_check
                                                   CHECK (amount_minor >= 0),
    currency                          char(3)      NOT NULL CONSTRAINT wager_transactions_currency_check
                                                   CHECK (currency ~ '^[A-Z]{3}$'),
    provider_id                       varchar(255),
    external_transaction_id           varchar(255),
    idempotency_key                   varchar(255),
    payload_hash                      char(64),
    round_id                          varchar(255),
    game_id                           varchar(255),
    reference_external_transaction_id varchar(255),
    reference_transaction_id          uuid         REFERENCES wager_transactions (id),
    reference_kind                    text         CONSTRAINT wager_transactions_reference_kind_check
                                                   CHECK (reference_kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    failure_code                      text,
    result_balance_minor              bigint,
    attempts                          integer      NOT NULL DEFAULT 0 CONSTRAINT wager_transactions_attempts_check
                                                   CHECK (attempts >= 0),
    next_attempt_at                   timestamptz,
    created_at                        timestamptz  NOT NULL,
    updated_at                        timestamptz  NOT NULL,
    processed_at                      timestamptz,

    CONSTRAINT wager_transactions_origin_shape CHECK (
        (origin = 'INTERNAL' AND kind = 'OPENING'
            AND provider_id IS NULL AND external_transaction_id IS NULL AND idempotency_key IS NULL
            AND payload_hash IS NULL AND round_id IS NULL AND game_id IS NULL
            AND reference_external_transaction_id IS NULL)
        OR
        (origin = 'EXTERNAL' AND kind <> 'OPENING'
            AND provider_id IS NOT NULL AND external_transaction_id IS NOT NULL AND idempotency_key IS NOT NULL
            AND payload_hash IS NOT NULL AND round_id IS NOT NULL AND game_id IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_status_shape CHECK (
        (status = 'PENDING' AND failure_code IS NULL)
        OR (status = 'PENDING_REFERENCE' AND failure_code IS NULL AND next_attempt_at IS NOT NULL)
        OR (status = 'PROCESSED' AND failure_code IS NULL AND result_balance_minor IS NOT NULL
            AND processed_at IS NOT NULL)
        OR (status IN ('REJECTED', 'FAILED') AND failure_code IS NOT NULL AND processed_at IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_failed_code CHECK (
        status <> 'FAILED' OR failure_code = 'INFRASTRUCTURE_FAILURE'
    ),
    CONSTRAINT wager_transactions_kind_amount CHECK (
        (kind = 'LOSS' AND amount_minor = 0) OR (kind <> 'LOSS' AND amount_minor > 0)
    ),
    CONSTRAINT wager_transactions_reversal_reference CHECK (
        kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_transaction_id IS NOT NULL
    ),
    CONSTRAINT wager_transactions_processed_reversal_resolved CHECK (
        status <> 'PROCESSED' OR kind NOT IN ('REFUND', 'ROLLBACK')
        OR (reference_transaction_id IS NOT NULL AND reference_kind IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_provider_idempotency_key UNIQUE (provider_id, idempotency_key),
    CONSTRAINT wager_transactions_provider_external_id UNIQUE (provider_id, external_transaction_id)
);

-- One OPENING credit per wallet.
CREATE UNIQUE INDEX wager_transactions_one_opening
    ON wager_transactions (wallet_id) WHERE kind = 'OPENING';

-- A reference never receives two successful reversals of the same kind.
CREATE UNIQUE INDEX wager_transactions_one_reversal_per_kind
    ON wager_transactions (reference_transaction_id, kind)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK');

-- A BET is reversed (REFUND or ROLLBACK) at most once, so its debit is never returned twice.
CREATE UNIQUE INDEX wager_transactions_one_reversal_per_bet
    ON wager_transactions (reference_transaction_id)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK') AND reference_kind = 'BET';

CREATE INDEX wager_transactions_pending_reference
    ON wager_transactions (next_attempt_at) WHERE status = 'PENDING_REFERENCE';

-- Enforces the state machine and the immutability of identity columns.
CREATE FUNCTION wager_transactions_guard_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('PROCESSED', 'REJECTED', 'FAILED') THEN
        RAISE EXCEPTION 'wager transaction % is terminal (%)', OLD.id, OLD.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
           (OLD.status = 'PENDING' AND NEW.status IN ('PROCESSED', 'REJECTED', 'PENDING_REFERENCE', 'FAILED'))
        OR (OLD.status = 'PENDING_REFERENCE' AND NEW.status IN ('PROCESSED', 'REJECTED', 'FAILED'))
    ) THEN
        RAISE EXCEPTION 'invalid wager transaction transition % -> %', OLD.status, NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF (NEW.id, NEW.origin, NEW.kind, NEW.wallet_id, NEW.player_id, NEW.amount_minor, NEW.currency,
        NEW.provider_id, NEW.external_transaction_id, NEW.idempotency_key, NEW.payload_hash,
        NEW.round_id, NEW.game_id, NEW.reference_external_transaction_id, NEW.created_at)
       IS DISTINCT FROM
       (OLD.id, OLD.origin, OLD.kind, OLD.wallet_id, OLD.player_id, OLD.amount_minor, OLD.currency,
        OLD.provider_id, OLD.external_transaction_id, OLD.idempotency_key, OLD.payload_hash,
        OLD.round_id, OLD.game_id, OLD.reference_external_transaction_id, OLD.created_at)
    THEN
        RAISE EXCEPTION 'identity columns of wager transaction % are immutable', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER wager_transactions_guard_update
    BEFORE UPDATE ON wager_transactions
    FOR EACH ROW EXECUTE FUNCTION wager_transactions_guard_update();

GRANT SELECT, INSERT, UPDATE ON wager_transactions TO wallet_app;

COMMIT;
