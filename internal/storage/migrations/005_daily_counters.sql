-- Per-installation daily usage counters (UTC date).
-- Used to enforce MaxCallsPerDay and MaxCostPerDayUSD guardrails,
-- which are independent of run_id — making them non-bypassable by callers.
CREATE TABLE installation_daily_counters (
    day_utc   TEXT NOT NULL,   -- YYYY-MM-DD UTC
    calls     INTEGER NOT NULL DEFAULT 0,
    cost_usd  TEXT NOT NULL DEFAULT '0',
    PRIMARY KEY (day_utc)
);
