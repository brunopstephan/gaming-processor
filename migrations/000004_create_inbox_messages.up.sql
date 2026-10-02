BEGIN;

-- One row per (consumer, message) whose handling committed. It is written in
-- the same SQL transaction as the domain changes it caused.
CREATE TABLE inbox_messages (
    consumer_name varchar(100) NOT NULL,
    message_id    varchar(255) NOT NULL,
    payload_hash  char(64)     NOT NULL,
    received_at   timestamptz  NOT NULL,
    processed_at  timestamptz  NOT NULL,
    CONSTRAINT inbox_messages_pkey PRIMARY KEY (consumer_name, message_id)
);

GRANT SELECT, INSERT ON inbox_messages TO wallet_app;

COMMIT;
