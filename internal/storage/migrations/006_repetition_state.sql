CREATE TABLE IF NOT EXISTS run_repetition_state (
    run_id        TEXT NOT NULL,
    kind          TEXT NOT NULL,
    count         INTEGER NOT NULL DEFAULT 0,
    last_values   TEXT NOT NULL DEFAULT '',
    updated_at_ns INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, kind)
);
