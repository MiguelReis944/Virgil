-- Stores the raw prompt and response content for each event.
-- Kept separate from the events table so telemetry schema stays clean.
CREATE TABLE IF NOT EXISTS event_content (
    event_id      TEXT PRIMARY KEY,
    prompt_json   TEXT,   -- serialized messages array from the request
    response_text TEXT,   -- assistant message text (success) or null
    error_message TEXT,   -- provider error body / error string (failure) or null
    created_at_unix_ns INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS event_content_created_at_idx ON event_content (created_at_unix_ns);
