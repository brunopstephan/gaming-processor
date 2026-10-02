BEGIN;

CREATE TABLE outbox_events (
    id              uuid         PRIMARY KEY,
    aggregate_type  varchar(50)  NOT NULL,
    aggregate_id    uuid         NOT NULL,
    event_type      varchar(100) NOT NULL,
    payload         jsonb        NOT NULL,
    occurred_at     timestamptz  NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0 CONSTRAINT outbox_events_attempts_check CHECK (attempts >= 0),
    next_attempt_at timestamptz  NOT NULL,
    locked_by       varchar(255),
    locked_until    timestamptz,
    published_at    timestamptz,
    last_error      text
);

CREATE INDEX outbox_events_unpublished ON outbox_events (next_attempt_at) WHERE published_at IS NULL;

-- The event snapshot is immutable; only delivery bookkeeping may change.
CREATE FUNCTION outbox_events_guard_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.aggregate_type, NEW.aggregate_id, NEW.event_type, NEW.payload, NEW.occurred_at)
       IS DISTINCT FROM
       (OLD.id, OLD.aggregate_type, OLD.aggregate_id, OLD.event_type, OLD.payload, OLD.occurred_at)
    THEN
        RAISE EXCEPTION 'outbox event % snapshot is immutable', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbox_events_guard_update
    BEFORE UPDATE ON outbox_events
    FOR EACH ROW EXECUTE FUNCTION outbox_events_guard_update();

GRANT SELECT, INSERT, UPDATE ON outbox_events TO wallet_app;

COMMIT;
