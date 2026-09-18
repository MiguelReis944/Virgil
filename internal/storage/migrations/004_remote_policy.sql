CREATE TABLE remote_policy_cache (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    version INTEGER NOT NULL CHECK (version > 0),
    payload BLOB NOT NULL
);
