package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// costExpr extracts cost from JSON payload (actual_cost or estimated_cost).
const costExpr = `COALESCE(CAST(json_extract(payload, '$.actual_cost') AS REAL), CAST(json_extract(payload, '$.estimated_cost') AS REAL), 0.0)`

// Summary holds aggregate metrics for the period.
type Summary struct {
	TotalCalls         int64
	TotalErrors        int64
	TotalCost          float64
	AvgLatMS           float64
	TotalInputTokens   int64
	TotalOutputTokens  int64
}

// UsageRow holds per-provider/model aggregated metrics.
type UsageRow struct {
	Provider string
	Model    string
	Calls    int64
	Errors   int64
	CostUSD  float64
	AvgLatMS float64
}

// HourPoint is one bar in the cost-per-hour chart.
type HourPoint struct {
	Label     string
	CostUSD   float64
	BarHeight int
	BarX      int
}

// RunRow is an aggregated row per run_id.
type RunRow struct {
	RunID    string
	Provider string
	Model    string
	Calls    int64
	CostUSD  float64
	AvgLatMS float64
	LastSeen time.Time
}

// EventRow is one event belonging to a run.
type EventRow struct {
	EventID      string
	Provider     string
	Model        string
	Status       string
	CostUSD      float64
	LatencyMS    int64
	InputTokens  int64
	OutputTokens int64
	CreatedAt    time.Time
}

// QuerySummary returns aggregate stats since the given time.
func QuerySummary(ctx context.Context, db *sql.DB, since time.Time) (Summary, error) {
	const q = `
SELECT
    COUNT(*) AS total_calls,
    SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END) AS total_errors,
    SUM(` + costExpr + `) AS total_cost,
    AVG(CAST(json_extract(payload, '$.latency_ms') AS REAL)) AS avg_lat,
    SUM(CAST(json_extract(payload, '$.input_tokens') AS INTEGER)) AS in_tok,
    SUM(CAST(json_extract(payload, '$.output_tokens') AS INTEGER)) AS out_tok
FROM events
WHERE created_at_unix_ns >= ?`
	row := db.QueryRowContext(ctx, q, since.UnixNano())
	var s Summary
	var errCount, inTok, outTok sql.NullInt64
	var cost, lat sql.NullFloat64
	if err := row.Scan(&s.TotalCalls, &errCount, &cost, &lat, &inTok, &outTok); err != nil {
		return Summary{}, fmt.Errorf("query summary: %w", err)
	}
	s.TotalErrors = errCount.Int64
	s.TotalCost = cost.Float64
	s.AvgLatMS = lat.Float64
	s.TotalInputTokens = inTok.Int64
	s.TotalOutputTokens = outTok.Int64
	return s, nil
}

// QueryUsage returns per-provider/model rows filtered by optional model/provider.
func QueryUsage(ctx context.Context, db *sql.DB, since time.Time, model, provider string) ([]UsageRow, error) {
	q := `
SELECT
    json_extract(payload, '$.provider')        AS provider,
    json_extract(payload, '$.requested_model') AS model,
    COUNT(*)                                   AS calls,
    SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END) AS errors,
    SUM(` + costExpr + `)                      AS cost,
    AVG(CAST(json_extract(payload, '$.latency_ms') AS REAL)) AS avg_lat
FROM events
WHERE created_at_unix_ns >= ?`
	args := []any{since.UnixNano()}
	if model != "" {
		q += ` AND json_extract(payload, '$.requested_model') = ?`
		args = append(args, model)
	}
	if provider != "" {
		q += ` AND json_extract(payload, '$.provider') = ?`
		args = append(args, provider)
	}
	q += ` GROUP BY provider, model ORDER BY cost DESC`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		var prov, mod sql.NullString
		var cost, lat sql.NullFloat64
		if err := rows.Scan(&prov, &mod, &r.Calls, &r.Errors, &cost, &lat); err != nil {
			return nil, err
		}
		r.Provider = prov.String
		r.Model = mod.String
		r.CostUSD = cost.Float64
		r.AvgLatMS = lat.Float64
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryHourlySeries returns bar chart points for the last `hours` hours.
func QueryHourlySeries(ctx context.Context, db *sql.DB, hours int) ([]HourPoint, error) {
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	const q = `
SELECT
    strftime('%H:00', datetime(created_at_unix_ns / 1000000000, 'unixepoch')) AS hour_label,
    SUM(` + costExpr + `) AS cost
FROM events
WHERE created_at_unix_ns >= ?
GROUP BY hour_label
ORDER BY MIN(created_at_unix_ns)`

	rows, err := db.QueryContext(ctx, q, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query hourly: %w", err)
	}
	defer rows.Close()

	var pts []HourPoint
	var maxCost float64
	for rows.Next() {
		var p HourPoint
		var cost sql.NullFloat64
		if err := rows.Scan(&p.Label, &cost); err != nil {
			return nil, err
		}
		p.CostUSD = cost.Float64
		if p.CostUSD > maxCost {
			maxCost = p.CostUSD
		}
		pts = append(pts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range pts {
		if maxCost > 0 {
			pts[i].BarHeight = int(pts[i].CostUSD / maxCost * 100)
		}
		pts[i].BarX = i * 24
	}
	return pts, nil
}

// QueryRuns returns aggregated rows per run_id, most recent first.
func QueryRuns(ctx context.Context, db *sql.DB, since time.Time) ([]RunRow, error) {
	const q = `
SELECT
    json_extract(payload, '$.run_id')          AS run_id,
    json_extract(payload, '$.provider')        AS provider,
    json_extract(payload, '$.requested_model') AS model,
    COUNT(*)                                   AS calls,
    SUM(` + costExpr + `)                      AS cost,
    AVG(CAST(json_extract(payload, '$.latency_ms') AS REAL)) AS avg_lat,
    MAX(created_at_unix_ns)                    AS last_ns
FROM events
WHERE created_at_unix_ns >= ?
  AND json_extract(payload, '$.run_id') IS NOT NULL
  AND json_extract(payload, '$.run_id') != ''
GROUP BY run_id
ORDER BY last_ns DESC
LIMIT 50`

	rows, err := db.QueryContext(ctx, q, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query runs: %w", err)
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		var runID, prov, mod sql.NullString
		var cost, lat sql.NullFloat64
		var lastNS int64
		if err := rows.Scan(&runID, &prov, &mod, &r.Calls, &cost, &lat, &lastNS); err != nil {
			return nil, err
		}
		r.RunID = runID.String
		r.Provider = prov.String
		r.Model = mod.String
		r.CostUSD = cost.Float64
		r.AvgLatMS = lat.Float64
		r.LastSeen = time.Unix(0, lastNS)
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryRunEvents returns individual events for a run.
func QueryRunEvents(ctx context.Context, db *sql.DB, runID string) ([]EventRow, error) {
	const q = `
SELECT
    event_id,
    json_extract(payload, '$.provider')        AS provider,
    json_extract(payload, '$.requested_model') AS model,
    status,
    ` + costExpr + `                           AS cost,
    CAST(json_extract(payload, '$.latency_ms') AS INTEGER) AS lat,
    COALESCE(CAST(json_extract(payload, '$.input_tokens') AS INTEGER), 0)  AS in_tok,
    COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER), 0) AS out_tok,
    created_at_unix_ns
FROM events
WHERE json_extract(payload, '$.run_id') = ?
ORDER BY created_at_unix_ns DESC
LIMIT 200`

	rows, err := db.QueryContext(ctx, q, runID)
	if err != nil {
		return nil, fmt.Errorf("query run events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var prov, mod sql.NullString
		var cost sql.NullFloat64
		var lat sql.NullInt64
		var ns int64
		if err := rows.Scan(&e.EventID, &prov, &mod, &e.Status, &cost, &lat, &e.InputTokens, &e.OutputTokens, &ns); err != nil {
			return nil, err
		}
		e.Provider = prov.String
		e.Model = mod.String
		e.CostUSD = cost.Float64
		e.LatencyMS = lat.Int64
		e.CreatedAt = time.Unix(0, ns)
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryAllEvents returns recent events across all runs.
func QueryAllEvents(ctx context.Context, db *sql.DB, since time.Time, filterModel, filterProvider string) ([]EventRow, error) {
	q := `
SELECT
    event_id,
    json_extract(payload, '$.provider')        AS provider,
    json_extract(payload, '$.requested_model') AS model,
    status,
    ` + costExpr + `                           AS cost,
    CAST(json_extract(payload, '$.latency_ms') AS INTEGER) AS lat,
    COALESCE(CAST(json_extract(payload, '$.input_tokens') AS INTEGER), 0)  AS in_tok,
    COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER), 0) AS out_tok,
    created_at_unix_ns
FROM events
WHERE created_at_unix_ns >= ?`
	args := []any{since.UnixNano()}
	if filterModel != "" {
		q += ` AND json_extract(payload, '$.requested_model') = ?`
		args = append(args, filterModel)
	}
	if filterProvider != "" {
		q += ` AND json_extract(payload, '$.provider') = ?`
		args = append(args, filterProvider)
	}
	q += ` ORDER BY created_at_unix_ns DESC LIMIT 100`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query all events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var prov, mod sql.NullString
		var cost sql.NullFloat64
		var lat sql.NullInt64
		var ns int64
		if err := rows.Scan(&e.EventID, &prov, &mod, &e.Status, &cost, &lat, &e.InputTokens, &e.OutputTokens, &ns); err != nil {
			return nil, err
		}
		e.Provider = prov.String
		e.Model = mod.String
		e.CostUSD = cost.Float64
		e.LatencyMS = lat.Int64
		e.CreatedAt = time.Unix(0, ns)
		out = append(out, e)
	}
	return out, rows.Err()
}

// QueryEventByID returns a single event's metadata by event_id.
func QueryEventByID(ctx context.Context, db *sql.DB, eventID string) (EventRow, error) {
	const q = `
SELECT
    event_id,
    json_extract(payload, '$.provider')        AS provider,
    json_extract(payload, '$.requested_model') AS model,
    status,
    ` + costExpr + `                           AS cost,
    CAST(json_extract(payload, '$.latency_ms') AS INTEGER) AS lat,
    COALESCE(CAST(json_extract(payload, '$.input_tokens') AS INTEGER), 0)  AS in_tok,
    COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER), 0) AS out_tok,
    created_at_unix_ns
FROM events
WHERE event_id = ?`
	row := db.QueryRowContext(ctx, q, eventID)
	var e EventRow
	var prov, mod sql.NullString
	var cost sql.NullFloat64
	var lat sql.NullInt64
	var ns int64
	if err := row.Scan(&e.EventID, &prov, &mod, &e.Status, &cost, &lat, &e.InputTokens, &e.OutputTokens, &ns); err != nil {
		return EventRow{}, err
	}
	e.Provider = prov.String
	e.Model = mod.String
	e.CostUSD = cost.Float64
	e.LatencyMS = lat.Int64
	e.CreatedAt = time.Unix(0, ns)
	return e, nil
}

// QueryDistinctModels returns the distinct model names seen since `since`.
func QueryDistinctModels(ctx context.Context, db *sql.DB, since time.Time) ([]string, error) {
	const q = `
SELECT DISTINCT json_extract(payload, '$.requested_model')
FROM events
WHERE created_at_unix_ns >= ?
  AND json_extract(payload, '$.requested_model') IS NOT NULL
ORDER BY 1`
	return queryStrings(ctx, db, q, since.UnixNano())
}

// QueryDistinctProviders returns the distinct provider names seen since `since`.
func QueryDistinctProviders(ctx context.Context, db *sql.DB, since time.Time) ([]string, error) {
	const q = `
SELECT DISTINCT json_extract(payload, '$.provider')
FROM events
WHERE created_at_unix_ns >= ?
  AND json_extract(payload, '$.provider') IS NOT NULL
ORDER BY 1`
	return queryStrings(ctx, db, q, since.UnixNano())
}

func queryStrings(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
