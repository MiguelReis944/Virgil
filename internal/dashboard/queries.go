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
	TotalCalls        int64
	TotalErrors       int64
	TotalCost         float64
	AvgLatMS          float64
	TotalInputTokens  int64
	TotalOutputTokens int64
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

// HourPoint is one bar/point in the hourly charts.
type HourPoint struct {
	Label     string
	CostUSD   float64
	Calls     int64
	Errors    int64
	AvgLatMS  float64
	InTokens  int64
	OutTokens int64
	// render helpers
	BarX  int
	BarW  int // bar width in SVG units
	BarCX int // bar center X for line chart points
	// normalised heights 0-80 (chart area height)
	BarHeight int // cost
	CallsH    int
	ErrRateH  int
	LatH      int
	InTokH    int
	OutTokH   int
}

// SessionRow is events grouped into a 1-minute bucket per model.
type SessionRow struct {
	BucketTS  int64 // unix seconds, start of the 1-min window
	Model     string
	Providers string // comma-separated distinct providers
	Calls     int64
	Errors    int64
	CostUSD   float64
	AvgLatMS  float64
	InTokens  int64
	OutTokens int64
	FirstSeen time.Time
	LastSeen  time.Time
}

// RunRow is an aggregated row per run_id (kept for agent use cases).
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

// QueryHourlySeries returns bar/line chart points for the last `hours` hours.
func QueryHourlySeries(ctx context.Context, db *sql.DB, hours int) ([]HourPoint, error) {
	return QueryHourlySeriesAt(ctx, db, hours, time.Now())
}

// QueryHourlySeriesAt is QueryHourlySeries with an injectable clock for deterministic tests.
func QueryHourlySeriesAt(ctx context.Context, db *sql.DB, hours int, now time.Time) ([]HourPoint, error) {
	since := now.Add(-time.Duration(hours) * time.Hour)
	labelExpr := `strftime('%H:00', datetime(created_at_unix_ns / 1000000000, 'unixepoch', 'localtime'))`
	if hours > 24 {
		labelExpr = `strftime('%m-%d %H:00', datetime(created_at_unix_ns / 1000000000, 'unixepoch', 'localtime'))`
	}
	q := `
SELECT
    ` + labelExpr + ` AS hour_label,
    SUM(` + costExpr + `)                                                       AS cost,
    COUNT(*)                                                                    AS calls,
    SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END)                       AS errors,
    AVG(CAST(json_extract(payload, '$.latency_ms') AS REAL))                   AS avg_lat,
    SUM(COALESCE(CAST(json_extract(payload, '$.input_tokens')  AS INTEGER),0)) AS in_tok,
    SUM(COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER),0)) AS out_tok
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
	for rows.Next() {
		var p HourPoint
		var cost, lat sql.NullFloat64
		var calls, errors, inTok, outTok sql.NullInt64
		if err := rows.Scan(&p.Label, &cost, &calls, &errors, &lat, &inTok, &outTok); err != nil {
			return nil, err
		}
		p.CostUSD = cost.Float64
		p.Calls = calls.Int64
		p.Errors = errors.Int64
		p.AvgLatMS = lat.Float64
		p.InTokens = inTok.Int64
		p.OutTokens = outTok.Int64
		pts = append(pts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// compute bar geometry: chart area is x=50..570 (520px), y=15..95 (80px)
	const chartX0, chartW, chartH = 50, 520, 80
	n := len(pts)
	if n == 0 {
		return pts, nil
	}
	slot := chartW / n
	bw := slot - 4
	if bw < 3 {
		bw = 3
	}
	if bw > 22 {
		bw = 22
	}

	maxCost, maxCalls, maxLat, maxInTok, maxOutTok := 0.0, int64(0), 0.0, int64(0), int64(0)
	for _, p := range pts {
		if p.CostUSD > maxCost {
			maxCost = p.CostUSD
		}
		if p.Calls > maxCalls {
			maxCalls = p.Calls
		}
		if p.AvgLatMS > maxLat {
			maxLat = p.AvgLatMS
		}
		if p.InTokens > maxInTok {
			maxInTok = p.InTokens
		}
		if p.OutTokens > maxOutTok {
			maxOutTok = p.OutTokens
		}
	}
	norm := func(v, max float64) int {
		if max == 0 {
			return 0
		}
		h := int(v / max * float64(chartH))
		if h < 1 && v > 0 {
			h = 1
		}
		return h
	}
	for i := range pts {
		pts[i].BarX = chartX0 + i*slot + (slot-bw)/2
		pts[i].BarW = bw
		pts[i].BarCX = chartX0 + i*slot + slot/2
		pts[i].BarHeight = norm(pts[i].CostUSD, maxCost)
		pts[i].CallsH = norm(float64(pts[i].Calls), float64(maxCalls))
		pts[i].LatH = norm(pts[i].AvgLatMS, maxLat)
		pts[i].InTokH = norm(float64(pts[i].InTokens), float64(maxInTok))
		pts[i].OutTokH = norm(float64(pts[i].OutTokens), float64(maxOutTok))
		if pts[i].Calls > 0 {
			pts[i].ErrRateH = norm(float64(pts[i].Errors)/float64(pts[i].Calls)*100, 100)
		}
	}
	return pts, nil
}

// SessionFilter holds optional filters for session queries.
type SessionFilter struct {
	Model    string
	Provider string
	Status   string // "success", "error", or "" for all
	MinLatMS int64
	MaxLatMS int64
}

// QuerySessions returns events grouped into 1-minute buckets per model, most recent first.
func QuerySessions(ctx context.Context, db *sql.DB, since time.Time, f SessionFilter) ([]SessionRow, error) {
	q := `
SELECT
    (created_at_unix_ns / 1000000000 / 60) * 60              AS bucket_ts,
    json_extract(payload, '$.requested_model')                AS model,
    GROUP_CONCAT(DISTINCT json_extract(payload, '$.provider')) AS providers,
    COUNT(*)                                                  AS calls,
    SUM(CASE WHEN status != 'success' THEN 1 ELSE 0 END)     AS errors,
    SUM(` + costExpr + `)                                     AS cost,
    AVG(CAST(json_extract(payload, '$.latency_ms') AS REAL))  AS avg_lat,
    SUM(COALESCE(CAST(json_extract(payload, '$.input_tokens') AS INTEGER),0))  AS in_tok,
    SUM(COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER),0)) AS out_tok,
    MIN(created_at_unix_ns)                                   AS first_ns,
    MAX(created_at_unix_ns)                                   AS last_ns
FROM events
WHERE created_at_unix_ns >= ?`
	args := []any{since.UnixNano()}
	if f.Model != "" {
		q += ` AND json_extract(payload, '$.requested_model') = ?`
		args = append(args, f.Model)
	}
	if f.Provider != "" {
		q += ` AND json_extract(payload, '$.provider') = ?`
		args = append(args, f.Provider)
	}
	if f.Status == "success" {
		q += ` AND status = 'success'`
	} else if f.Status == "error" {
		q += ` AND status != 'success'`
	}
	if f.MinLatMS > 0 {
		q += ` AND CAST(json_extract(payload, '$.latency_ms') AS INTEGER) >= ?`
		args = append(args, f.MinLatMS)
	}
	if f.MaxLatMS > 0 {
		q += ` AND CAST(json_extract(payload, '$.latency_ms') AS INTEGER) <= ?`
		args = append(args, f.MaxLatMS)
	}
	q += `
GROUP BY bucket_ts, model
ORDER BY bucket_ts DESC, model ASC
LIMIT 200`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var s SessionRow
		var model, providers sql.NullString
		var cost, lat sql.NullFloat64
		var firstNS, lastNS int64
		if err := rows.Scan(&s.BucketTS, &model, &providers, &s.Calls, &s.Errors, &cost, &lat, &s.InTokens, &s.OutTokens, &firstNS, &lastNS); err != nil {
			return nil, err
		}
		s.Model = model.String
		s.Providers = providers.String
		s.CostUSD = cost.Float64
		s.AvgLatMS = lat.Float64
		s.FirstSeen = time.Unix(0, firstNS)
		s.LastSeen = time.Unix(0, lastNS)
		out = append(out, s)
	}
	return out, rows.Err()
}

// QuerySessionEvents returns all events in a 1-minute bucket for a specific model.
func QuerySessionEvents(ctx context.Context, db *sql.DB, bucketTS int64, model string) ([]EventRow, error) {
	bucketNSStart := bucketTS * 1_000_000_000
	bucketNSEnd := (bucketTS + 60) * 1_000_000_000
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
WHERE created_at_unix_ns >= ? AND created_at_unix_ns < ?`
	args := []any{bucketNSStart, bucketNSEnd}
	if model != "" {
		q += ` AND json_extract(payload, '$.requested_model') = ?`
		args = append(args, model)
	}
	q += ` ORDER BY created_at_unix_ns ASC`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query session events: %w", err)
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
