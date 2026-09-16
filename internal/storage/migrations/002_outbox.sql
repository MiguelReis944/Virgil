CREATE TABLE outbox (
    destination TEXT NOT NULL,
    event_id TEXT NOT NULL REFERENCES events (event_id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'sending', 'delivered', 'retryable', 'dead_letter')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_unix_ns INTEGER NOT NULL,
    lease_until_unix_ns INTEGER,
    last_error_code TEXT,
    PRIMARY KEY (destination, event_id)
);

CREATE INDEX outbox_ready_idx ON outbox (destination, state, next_attempt_unix_ns);
