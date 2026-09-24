package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

const executionsBody = `{{define "content"}}
<form class="filter-bar" method="get" action="/dashboard/executions">
  <label>State <select name="state"><option value="">All states</option>{{range .States}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}</select></label>
  <label>Period <input name="hours" type="number" min="1" max="168" value="{{.Hours}}"> hours</label>
  <label>Rows <input name="limit" type="number" min="1" max="200" value="{{.Limit}}"></label>
  <button type="submit">Apply</button>
</form>
<div class="table-wrap"><table><thead><tr><th>Execution</th><th>State</th><th>Started</th><th>Duration</th><th>Provider calls</th><th>Tokens</th><th>Recorded cost</th><th>Policy / reason</th><th>Termination</th></tr></thead><tbody>
{{range .Rows}}<tr><td><a class="run-link" href="/dashboard/executions/{{.RunID}}">{{.RunID}}</a></td><td><span class="badge {{.BadgeClass}}">{{.StateLabel}}</span></td><td>{{.Started}}</td><td>{{.Duration}}</td><td>{{.ProviderCalls}}</td><td>{{.Tokens}}</td><td>{{.RecordedCost}}</td><td>{{.PolicyOutcome}}</td><td>{{.TerminationOutcome}}</td></tr>
{{else}}<tr><td class="no-data" colspan="9">No executions match these filters. Start an agent with <code>virgil run</code> to create one.</td></tr>{{end}}
</tbody></table></div>{{end}}`

const executionDetailBody = `{{define "content"}}
<div class="cards">
  <div class="card"><div class="card-label">State</div><div class="card-value {{.StateValueClass}}">{{.StateLabel}}</div></div>
  <div class="card"><div class="card-label">Started</div><div class="card-sub">{{.Started}}</div><div class="card-value val-white">{{.Duration}}</div></div>
  <div class="card"><div class="card-label">Provider calls</div><div class="card-value val-blue">{{.ProviderCalls}}</div></div>
  <div class="card"><div class="card-label">Total tokens</div><div class="card-value val-purple">{{.Tokens}}</div></div>
  <div class="card"><div class="card-label">Recorded cost</div><div class="card-value val-white">{{.RecordedCost}}</div></div>
</div>
<div class="card"><div class="card-label">Lifecycle</div><dl class="detail-list"><dt>Execution ID</dt><dd class="mono">{{.RunID}}</dd><dt>Stop reason</dt><dd>{{.StopReason}}</dd><dt>Exit code</dt><dd>{{.ExitCode}}</dd></dl></div>
{{if .HasPolicy}}<h2 class="section-title">Circuit break</h2><div class="card risk-card"><dl class="detail-list"><dt>Outcome</dt><dd>Request blocked</dd><dt>Policy</dt><dd>{{.Policy}}</dd><dt>Reason</dt><dd>{{.PolicyReason}}</dd><dt>Attempt</dt><dd>{{.PolicyAttempt}}</dd><dt>Threshold</dt><dd>{{.PolicyThreshold}}</dd>{{if .BlockedCallEstimate}}<dt>Estimated blocked call cost</dt><dd>{{.BlockedCallEstimate}} USD</dd>{{end}}</dl></div>{{end}}
<h2 class="section-title">Termination</h2><div class="card"><dl class="detail-list"><dt>Result</dt><dd>{{.TerminationLabel}}</dd>{{if .TerminationError}}<dt>Error code</dt><dd>{{.TerminationError}}</dd>{{end}}</dl></div>
<h2 class="section-title">Event timeline</h2><div class="table-wrap"><table><thead><tr><th>Time</th><th>Status</th><th>Provider</th><th>Model</th><th>Tokens</th><th>Latency</th></tr></thead><tbody>
{{range .Events}}<tr><td>{{.Created}}</td><td><span class="badge {{.BadgeClass}}">{{.Status}}</span></td><td>{{.Provider}}</td><td>{{.Model}}</td><td>{{.Tokens}}</td><td>{{.Latency}}</td></tr>{{else}}<tr><td class="no-data" colspan="6">No request events were recorded for this execution.</td></tr>{{end}}
</tbody></table>{{if .TimelineNotice}}<div class="show-more-wrap">{{.TimelineNotice}}</div>{{end}}</div>{{end}}`

type executionListRow struct {
	RunID              string
	StateLabel         string
	BadgeClass         string
	Started            string
	Duration           string
	ProviderCalls      string
	Tokens             string
	RecordedCost       string
	PolicyOutcome      string
	TerminationOutcome string
}

type executionMetrics struct {
	ProviderCalls int64
	TotalEvents   int64
	TotalTokens   int64
	RecordedCost  float64
}

type executionStateOption struct {
	Value    string
	Label    string
	Selected bool
}

type executionEventRow struct {
	Created    string
	Status     string
	BadgeClass string
	Provider   string
	Model      string
	Tokens     string
	Latency    string
}

// ExecutionsHandler renders the durable supervised execution history.
func ExecutionsHandler(db *sql.DB, password string, clock func() time.Time) http.Handler {
	if clock == nil {
		clock = time.Now
	}
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter, hours, err := parseExecutionFilters(r)
		if err != nil {
			http.Error(w, "invalid execution filters", http.StatusBadRequest)
			return
		}
		filter.Since = clock().UTC().Add(-time.Duration(hours) * time.Hour)
		rows, err := storage.ListExecutionsFiltered(r.Context(), db, filter)
		if err != nil {
			http.Error(w, "execution history unavailable", http.StatusInternalServerError)
			return
		}
		runIDs := make([]string, len(rows))
		for index := range rows {
			runIDs[index] = rows[index].RunID
		}
		metrics, err := queryExecutionMetrics(r.Context(), db, runIDs)
		if err != nil {
			http.Error(w, "execution metrics unavailable", http.StatusInternalServerError)
			return
		}
		data := struct {
			Rows   []executionListRow
			States []executionStateOption
			Hours  int
			Limit  int
		}{Hours: hours, Limit: filter.Limit, States: executionStateOptions(filter.State)}
		for _, row := range rows {
			label, badge, _ := executionStatePresentation(row.State)
			rowMetrics := metrics[row.RunID]
			data.Rows = append(data.Rows, executionListRow{
				RunID: row.RunID, StateLabel: label, BadgeClass: badge,
				Started: row.StartedAt.Local().Format("2006-01-02 15:04:05"), Duration: executionDuration(row, clock()),
				ProviderCalls: providerCallsLabel(rowMetrics.ProviderCalls), Tokens: tokenCountLabel(rowMetrics.TotalTokens),
				RecordedCost: fmt.Sprintf("$%.4f", rowMetrics.RecordedCost), PolicyOutcome: policyOutcome(row),
				TerminationOutcome: terminationOutcome(row),
			})
		}
		if err := renderPage(w, pageData{Title: "Executions", ActiveSection: sectionExecutions, HasAuth: password != ""}, executionsBody, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}))
}

// ExecutionHandler renders one execution and its request event timeline.
func ExecutionHandler(db *sql.DB, password string) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("runID")
		if !validExecutionRunID(runID) {
			http.Error(w, "invalid execution ID", http.StatusBadRequest)
			return
		}
		execution, err := storage.Execution(r.Context(), db, runID)
		if errors.Is(err, executions.ErrRunNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "execution unavailable", http.StatusInternalServerError)
			return
		}
		events, err := QueryRunEvents(r.Context(), db, runID)
		if err != nil {
			http.Error(w, "execution events unavailable", http.StatusInternalServerError)
			return
		}
		metricsByRun, err := queryExecutionMetrics(r.Context(), db, []string{runID})
		if err != nil {
			http.Error(w, "execution metrics unavailable", http.StatusInternalServerError)
			return
		}
		metrics := metricsByRun[runID]
		label, _, valueClass := executionStatePresentation(execution.State)
		data := struct {
			RunID, StateLabel, StateValueClass, Started, Duration string
			StopReason, ExitCode                                  string
			ProviderCalls                                         int64
			Tokens, RecordedCost, TimelineNotice                  string
			HasPolicy                                             bool
			Policy, PolicyReason                                  string
			PolicyAttempt, PolicyThreshold                        string
			BlockedCallEstimate                                   string
			TerminationLabel, TerminationError                    string
			Events                                                []executionEventRow
		}{
			RunID: execution.RunID, StateLabel: label, StateValueClass: valueClass,
			Started: execution.StartedAt.Local().Format("2006-01-02 15:04:05"), Duration: executionDuration(execution, time.Now()),
			StopReason: valueOrDash(execution.StopReason), ExitCode: intPointerLabel(execution.ExitCode),
			ProviderCalls: metrics.ProviderCalls, Tokens: tokenCountLabel(metrics.TotalTokens), RecordedCost: fmt.Sprintf("$%.4f", metrics.RecordedCost),
			HasPolicy: execution.Policy != "", Policy: execution.Policy, PolicyReason: execution.PolicyReason,
			PolicyAttempt: int64PointerLabel(execution.PolicyAttempt), PolicyThreshold: int64PointerLabel(execution.PolicyThreshold),
			BlockedCallEstimate: execution.BlockedCallEstimateUSD,
			TerminationLabel:    terminationOutcome(execution), TerminationError: execution.TerminationErrorCode,
		}
		if metrics.TotalEvents > int64(len(events)) {
			data.TimelineNotice = fmt.Sprintf("Showing newest %d of %d events", len(events), metrics.TotalEvents)
		}
		for _, event := range events {
			status := eventStatusLabel(event.Status)
			badge := "badge-success"
			if event.Status != "success" {
				badge = "badge-err"
			}
			data.Events = append(data.Events, executionEventRow{
				Created: event.CreatedAt.Local().Format("2006-01-02 15:04:05"), Status: status, BadgeClass: badge,
				Provider: valueOrDash(event.Provider), Model: valueOrDash(event.Model),
				Tokens: fmt.Sprintf("%d in / %d out", event.InputTokens, event.OutputTokens), Latency: fmt.Sprintf("%d ms", event.LatencyMS),
			})
		}
		if err := renderPage(w, pageData{Title: "Execution " + runID, ActiveSection: sectionExecutions, HasAuth: password != ""}, executionDetailBody, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}))
}

// LegacyRunRedirectHandler keeps historical dashboard links useful.
func LegacyRunRedirectHandler(password string) http.Handler {
	return requirePanelAuth(password, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("runID")
		if !validExecutionRunID(runID) {
			http.Error(w, "invalid execution ID", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard/executions/"+url.PathEscape(runID), http.StatusMovedPermanently)
	}))
}

func parseExecutionFilters(r *http.Request) (storage.ExecutionListFilter, int, error) {
	query := r.URL.Query()
	state := executions.State(query.Get("state"))
	if state != "" && !validExecutionStateFilter(state) {
		return storage.ExecutionListFilter{}, 0, errors.New("invalid state")
	}
	hours, err := boundedQueryInt(query.Get("hours"), 24, 1, 168)
	if err != nil {
		return storage.ExecutionListFilter{}, 0, err
	}
	limit, err := boundedQueryInt(query.Get("limit"), 50, 1, 200)
	if err != nil {
		return storage.ExecutionListFilter{}, 0, err
	}
	return storage.ExecutionListFilter{State: state, Limit: limit}, hours, nil
}

func boundedQueryInt(raw string, fallback, minimum, maximum int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("value outside allowed range")
	}
	return value, nil
}

func validExecutionStateFilter(state executions.State) bool {
	for _, candidate := range []executions.State{
		executions.StateStarting, executions.StateRunning, executions.StateCompleted, executions.StateFailed,
		executions.StateBlocked, executions.StateDeadline, executions.StateInterrupted,
		executions.StateGatewayFailed, executions.StateTerminationFailed,
	} {
		if state == candidate {
			return true
		}
	}
	return false
}

func validExecutionRunID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func executionStateOptions(selected executions.State) []executionStateOption {
	states := []executions.State{
		executions.StateStarting, executions.StateRunning, executions.StateCompleted, executions.StateFailed,
		executions.StateBlocked, executions.StateDeadline, executions.StateInterrupted,
		executions.StateGatewayFailed, executions.StateTerminationFailed,
	}
	out := make([]executionStateOption, 0, len(states))
	for _, state := range states {
		label, _, _ := executionStatePresentation(state)
		out = append(out, executionStateOption{Value: string(state), Label: label, Selected: selected == state})
	}
	return out
}

func executionStatePresentation(state executions.State) (label, badge, valueClass string) {
	label = strings.NewReplacer("_", " ").Replace(string(state))
	label = strings.Title(label) //nolint:staticcheck // labels are a fixed ASCII enum.
	switch state {
	case executions.StateCompleted:
		return label, "badge-success", "val-green"
	case executions.StateRunning, executions.StateStarting:
		return label, "badge-openai", "val-blue"
	case executions.StateBlocked, executions.StateFailed, executions.StateGatewayFailed, executions.StateTerminationFailed:
		return label, "badge-err", "val-red"
	default:
		return label, "badge-anthropic", "val-yellow"
	}
}

func executionDuration(execution executions.Execution, now time.Time) string {
	end := now.UTC()
	if execution.EndedAt != nil {
		end = execution.EndedAt.UTC()
	}
	duration := end.Sub(execution.StartedAt.UTC())
	if duration < 0 {
		duration = 0
	}
	return duration.Round(time.Second).String()
}

func queryExecutionMetrics(ctx context.Context, db *sql.DB, runIDs []string) (map[string]executionMetrics, error) {
	metrics := make(map[string]executionMetrics, len(runIDs))
	if len(runIDs) == 0 {
		return metrics, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(runIDs)), ",")
	args := make([]any, len(runIDs))
	for index := range runIDs {
		args[index] = runIDs[index]
	}
	query := `SELECT
		json_extract(payload, '$.run_id') AS run_id,
		SUM(CASE WHEN status != 'policy_block' THEN 1 ELSE 0 END) AS provider_calls,
		COUNT(*) AS total_events,
		SUM(COALESCE(CAST(json_extract(payload, '$.input_tokens') AS INTEGER), 0) +
			COALESCE(CAST(json_extract(payload, '$.output_tokens') AS INTEGER), 0)) AS total_tokens,
		SUM(` + costExpr + `) AS recorded_cost
		FROM events
		WHERE json_extract(payload, '$.run_id') IN (` + placeholders + `)
		GROUP BY run_id`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution metrics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID string
		var row executionMetrics
		if err := rows.Scan(&runID, &row.ProviderCalls, &row.TotalEvents, &row.TotalTokens, &row.RecordedCost); err != nil {
			return nil, fmt.Errorf("scan execution metrics: %w", err)
		}
		metrics[runID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution metrics: %w", err)
	}
	return metrics, nil
}

func policyOutcome(execution executions.Execution) string {
	if execution.Policy == "" {
		return "—"
	}
	if execution.PolicyReason == "" {
		return execution.Policy
	}
	return execution.Policy + " / " + execution.PolicyReason
}

func providerCallsLabel(count int64) string {
	if count == 1 {
		return "1 provider call"
	}
	return fmt.Sprintf("%d provider calls", count)
}

func tokenCountLabel(count int64) string {
	return fmt.Sprintf("%d tokens", count)
}

func terminationOutcome(execution executions.Execution) string {
	if execution.State == executions.StateTerminationFailed || execution.TerminationStatus == executions.TerminationFailed {
		return "Termination failed"
	}
	switch execution.TerminationStatus {
	case executions.TerminationSucceeded:
		return "Process tree terminated"
	case executions.TerminationNotRequired:
		return "Not required"
	default:
		return "Pending"
	}
}

func eventStatusLabel(status string) string {
	if status == "policy_block" {
		return "Policy block"
	}
	return strings.Title(strings.ReplaceAll(status, "_", " ")) //nolint:staticcheck // fixed event enum.
}

func valueOrDash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func intPointerLabel(value *int) string {
	if value == nil {
		return "—"
	}
	return strconv.Itoa(*value)
}

func int64PointerLabel(value *int64) string {
	if value == nil {
		return "—"
	}
	return strconv.FormatInt(*value, 10)
}
