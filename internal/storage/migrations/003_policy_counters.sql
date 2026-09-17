CREATE TABLE policy_runs (
    run_id TEXT PRIMARY KEY,
    started_at_unix_ns INTEGER NOT NULL,
    calls INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    tool_calls INTEGER NOT NULL DEFAULT 0,
    cost_usd TEXT NOT NULL DEFAULT '0'
);

CREATE TABLE policy_reservations (
    reservation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES policy_runs(run_id),
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    tool_calls INTEGER NOT NULL,
    cost_usd TEXT NOT NULL,
    reconciled INTEGER NOT NULL DEFAULT 0
);
