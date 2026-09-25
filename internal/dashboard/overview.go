package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type overviewData struct {
	Hours               int
	ActiveExecutions    int64
	BlockedExecutions   int64
	SuccessfulBreaks    int64
	TerminationFailures int64
	TotalCalls          int64
	RecordedCost        string
}

const overviewBody = `{{define "content"}}
<div class="section-head"><div><h2>Execution safety</h2><p>Live supervision and circuit-break outcomes for the selected period.</p></div><div class="filter-bar"><span>Last {{.Hours}} hours</span><a class="btn-sm" href="/dashboard?hours=24">24h</a><a class="btn-sm" href="/dashboard?hours=168">7d</a></div></div>
<div class="metric-grid operational-grid">
<div class="card safe-card"><div class="card-label">Active executions</div><div class="card-value val-green">{{.ActiveExecutions}}</div><div class="card-sub">supervised now</div></div>
<div class="card risk-card"><div class="card-label">Blocked executions</div><div class="card-value val-red">{{.BlockedExecutions}}</div><div class="card-sub">policy trips in period</div></div>
<div class="card safe-card"><div class="card-label">Successful circuit breaks</div><div class="card-value val-green">{{.SuccessfulBreaks}}</div><div class="card-sub">process trees stopped</div></div>
<div class="card risk-card"><div class="card-label">Termination failures</div><div class="card-value val-red">{{.TerminationFailures}}</div><div class="card-sub">requires attention</div></div>
<div class="card"><div class="card-label">Total calls</div><div class="card-value val-white">{{.TotalCalls}}</div><div class="card-sub">recorded requests</div></div>
<div class="card"><div class="card-label">Recorded cost</div><div class="card-value val-blue">${{.RecordedCost}}</div><div class="card-sub">provider estimate</div></div>
</div>{{end}}`

func OverviewHandler(db *sql.DB, password string, providersConfigured ...bool) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(providersConfigured) > 0 && !providersConfigured[0] {
			http.Redirect(w, r, "/dashboard/providers?onboarding=1", http.StatusFound)
			return
		}
		hours := parseIntClamp(r.URL.Query().Get("hours"), 24, 1, 24*31)
		data, err := queryOverview(r.Context(), db, time.Now().Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			http.Error(w, "query error", http.StatusInternalServerError)
			return
		}
		data.Hours = hours
		if err := renderPage(w, pageData{Title: "Overview", ActiveSection: sectionOverview, HasAuth: password != ""}, overviewBody, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}))
}

func queryOverview(ctx context.Context, db *sql.DB, since time.Time) (overviewData, error) {
	var data overviewData
	err := db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN state IN ('starting','running') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN policy IS NOT NULL AND started_at_unix_ns >= ? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='blocked' AND termination_status='succeeded' AND started_at_unix_ns >= ? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN state='termination_failed' AND started_at_unix_ns >= ? THEN 1 ELSE 0 END),0)
		FROM executions`, since.UnixNano(), since.UnixNano(), since.UnixNano()).Scan(&data.ActiveExecutions, &data.BlockedExecutions, &data.SuccessfulBreaks, &data.TerminationFailures)
	if err != nil {
		return data, fmt.Errorf("query protection overview: %w", err)
	}
	summary, err := QuerySummary(ctx, db, since)
	if err != nil {
		return data, err
	}
	data.TotalCalls = summary.TotalCalls
	data.RecordedCost = strconv.FormatFloat(summary.TotalCost, 'f', 4, 64)
	return data, nil
}
