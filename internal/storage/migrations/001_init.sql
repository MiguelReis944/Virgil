CREATE TABLE installation (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    installation_id TEXT NOT NULL UNIQUE
);

CREATE TABLE events (
    event_id TEXT PRIMARY KEY,
    installation_id TEXT NOT NULL,
    created_at_unix_ns INTEGER NOT NULL,
    status TEXT NOT NULL,
    payload BLOB NOT NULL
);

CREATE INDEX events_created_at_idx ON events (created_at_unix_ns);
