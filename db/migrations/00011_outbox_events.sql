-- +goose Up
CREATE TABLE outbox_events
(
    id              UUID PRIMARY KEY      DEFAULT uuidv7(),

    aggregate_type  TEXT         NOT NULL,
    aggregate_id    UUID         NOT NULL,

    event_type      TEXT         NOT NULL,
    event_version   INTEGER      NOT NULL DEFAULT 1,

    topic           VARCHAR(255) NOT NULL,
    payload         JSONB        NOT NULL,

    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,

    trace_id        TEXT,

    attempt_count   INTEGER      NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,

    locked_at       TIMESTAMPTZ,
    locked_by       TEXT,

    last_error      TEXT
);

CREATE INDEX idx_outbox_events_pending
    ON outbox_events (created_at, id) WHERE published_at IS NULL;

CREATE INDEX idx_outbox_events_retry
    ON outbox_events (next_attempt_at, id) WHERE published_at IS NULL
      AND next_attempt_at IS NOT NULL;

CREATE INDEX idx_outbox_events_aggregate
    ON outbox_events (aggregate_type, aggregate_id);


-- +goose Down
DROP TABLE IF EXISTS outbox_events;