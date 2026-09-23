CREATE TABLE executions (
    run_id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN (
        'starting', 'running', 'completed', 'failed', 'blocked',
        'deadline', 'interrupted', 'gateway_failed', 'termination_failed'
    )),
    started_at_unix_ns INTEGER NOT NULL,
    ended_at_unix_ns INTEGER,
    exit_code INTEGER,
    stop_reason TEXT,
    policy TEXT,
    policy_reason TEXT,
    policy_attempt INTEGER CHECK (policy_attempt IS NULL OR policy_attempt >= 0),
    policy_threshold INTEGER CHECK (policy_threshold IS NULL OR policy_threshold >= 0),
    blocked_call_estimate_usd TEXT,
    termination_status TEXT CHECK (termination_status IN (
        'not_required', 'succeeded', 'failed'
    )),
    termination_error_code TEXT
);

CREATE INDEX executions_state_started_idx
    ON executions(state, started_at_unix_ns DESC);
