package dashboard

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestExecutionsHandlerRendersFilteredLifecycleHistory(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	now := time.Now().UTC()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/executions?state=blocked&hours=2&limit=10", nil)
	ExecutionsHandler(db, "", func() time.Time { return now }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"run_blocked", "Blocked", "1 provider call", "12 tokens", "$0.0030",
		"max_requests_per_run", "request_limit_exceeded", "Process tree terminated", "Executions",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	for _, unwanted := range []string{"run_running", "run_completed", "run_old_blocked"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body unexpectedly contains %q", unwanted)
		}
	}
	if !strings.Contains(body, `href="/dashboard/executions/run_blocked"`) {
		t.Error("execution row does not link to detail")
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Error("shared shell active navigation missing")
	}
	assertPanelSecurityHeaders(t, rec.Header())
}

func TestExecutionsHandlerOrdersAndLabelsLifecycleStates(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	rec := httptest.NewRecorder()
	ExecutionsHandler(db, "", time.Now).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/executions?hours=4", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	ordered := []string{"run_running", "run_completed", "run_blocked", "run_deadline", "run_termination_failed", "run_old_blocked"}
	last := -1
	for _, id := range ordered {
		index := strings.Index(body, id)
		if index <= last {
			t.Fatalf("execution %q is out of newest-first order", id)
		}
		last = index
	}
	for _, label := range []string{"Running", "Completed", "Blocked", "Deadline", "Termination Failed"} {
		if !strings.Contains(body, ">"+label+"<") {
			t.Errorf("body missing state label %q", label)
		}
	}
}

func TestExecutionsHandlerValidatesFilters(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	for _, query := range []string{
		"state=nope", "hours=0", "hours=169", "hours=abc", "limit=0", "limit=201", "limit=abc",
	} {
		t.Run(query, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/dashboard/executions?"+query, nil)
			ExecutionsHandler(db, "", time.Now).ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertPanelSecurityHeaders(t, rec.Header())
		})
	}
}

func TestExecutionPagesRequirePanelAuthentication(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	for _, handler := range []http.Handler{
		ExecutionsHandler(db, "secret", time.Now), ExecutionHandler(db, "secret"), LegacyRunRedirectHandler("secret"),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/executions", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
			t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		assertPanelSecurityHeaders(t, rec.Header())
	}
}

func TestExecutionsHandlerEscapesPersistedRunID(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	malicious := `run_"><img src=x onerror=alert(1)>`
	if _, err := db.Exec(`INSERT INTO executions(run_id,state,started_at_unix_ns) VALUES(?, 'running', ?)`, malicious, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ExecutionsHandler(db, "", time.Now).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/executions", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;img") {
		t.Fatalf("run ID was not safely escaped: %s", body)
	}
}

func TestExecutionHandlerRendersLifecyclePolicyTerminationAndTimeline(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/executions/run_blocked", nil)
	req.SetPathValue("runID", "run_blocked")
	ExecutionHandler(db, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"run_blocked", "Blocked", "max_requests_per_run", "request_limit_exceeded",
		"2", "1", "Estimated blocked call cost", "0.0042", "Request blocked",
		"Process tree terminated", "provider-a", "model-a", "Policy block", "12 tokens", "$0.0030",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "cost saved") {
		t.Error("detail makes an unsupported cost-saved claim")
	}
	assertPanelSecurityHeaders(t, rec.Header())
}

func TestExecutionHandlerUsesUntruncatedAggregateCounts(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	now := time.Now().UTC()
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("%032x", i+100)
		payload := fmt.Sprintf(`{"event_id":%q,"event_type":"llm.response.completed","schema_version":"1.0","created_at":%q,"installation_id":"install_test","run_id":"run_blocked","trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef","provider":"provider-a","requested_model":"model-a","input_tokens":1,"output_tokens":1,"cached_tokens":null,"usage_source":"provider","latency_ms":1,"status":"success","actual_cost":"0.001","estimated_cost":null,"content_capture":false}`, id, now.Format(time.RFC3339Nano))
		if _, err := db.Exec(`INSERT INTO events(event_id,installation_id,created_at_unix_ns,status,payload) VALUES(?,?,?,?,?)`, id, "install_test", now.Add(time.Duration(i)*time.Nanosecond).UnixNano(), "success", payload); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/executions/run_blocked", nil)
	req.SetPathValue("runID", "run_blocked")
	ExecutionHandler(db, "").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"202", "414 tokens", "$0.2040", "Showing newest 200 of 203 events"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing aggregate %q", want)
		}
	}
}

func TestExecutionHandlerMakesTerminationFailureExplicit(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/executions/run_termination_failed", nil)
	req.SetPathValue("runID", "run_termination_failed")
	ExecutionHandler(db, "").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Termination failed") {
		t.Fatal("termination failure is not explicit")
	}
	for _, misleading := range []string{"Process tree terminated", "protected"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(misleading)) {
			t.Fatalf("termination failure contains misleading claim %q", misleading)
		}
	}
}

func TestExecutionHandlerReturnsBadRequestAndNotFound(t *testing.T) {
	db := seedExecutionPageFixtures(t)
	tests := []struct {
		name string
		id   string
		want int
	}{
		{name: "invalid", id: "bad/id", want: http.StatusBadRequest},
		{name: "too long", id: strings.Repeat("a", 65), want: http.StatusBadRequest},
		{name: "unknown", id: "run_unknown", want: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/dashboard/executions/"+url.PathEscape(tc.id), nil)
			req.SetPathValue("runID", tc.id)
			ExecutionHandler(db, "").ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertPanelSecurityHeaders(t, rec.Header())
		})
	}
}

func TestLegacyRunHandlerRedirectsToExecutionDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/run/run_blocked", nil)
	req.SetPathValue("runID", "run_blocked")
	LegacyRunRedirectHandler("").ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/dashboard/executions/run_blocked" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func seedExecutionPageFixtures(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		journal.Close()
		_ = db.Close()
	})
	now := time.Now().UTC()
	fixtures := []struct {
		id        string
		state     string
		started   time.Time
		ended     *time.Time
		policy    string
		reason    string
		attempt   any
		threshold any
		estimate  any
		term      any
	}{
		{id: "run_running", state: "running", started: now.Add(-10 * time.Minute)},
		{id: "run_completed", state: "completed", started: now.Add(-20 * time.Minute), ended: timePointer(now.Add(-19 * time.Minute)), term: "not_required"},
		{id: "run_blocked", state: "blocked", started: now.Add(-30 * time.Minute), ended: timePointer(now.Add(-29 * time.Minute)), policy: "max_requests_per_run", reason: "request_limit_exceeded", attempt: 2, threshold: 1, estimate: "0.0042", term: "succeeded"},
		{id: "run_deadline", state: "deadline", started: now.Add(-40 * time.Minute), ended: timePointer(now.Add(-39 * time.Minute)), term: "succeeded"},
		{id: "run_termination_failed", state: "termination_failed", started: now.Add(-50 * time.Minute), ended: timePointer(now.Add(-49 * time.Minute)), term: "failed"},
		{id: "run_old_blocked", state: "blocked", started: now.Add(-3 * time.Hour), ended: timePointer(now.Add(-179 * time.Minute)), policy: "max_cost_per_run", reason: "cost_limit_exceeded", attempt: 2, threshold: 1},
	}
	for _, f := range fixtures {
		var ended any
		if f.ended != nil {
			ended = f.ended.UnixNano()
		}
		if _, err := db.Exec(`INSERT INTO executions(
			run_id,state,started_at_unix_ns,ended_at_unix_ns,policy,policy_reason,
			policy_attempt,policy_threshold,blocked_call_estimate_usd,termination_status,termination_error_code
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, f.id, f.state, f.started.UnixNano(), ended, nullString(f.policy), nullString(f.reason), f.attempt, f.threshold, f.estimate, f.term, nil); err != nil {
			t.Fatal(err)
		}
	}
	events := []struct {
		id      string
		status  string
		created time.Time
		payload string
	}{
		{
			id: "fedcba9876543210fedcba9876543210", status: "success", created: now.Add(-29*time.Minute - time.Second),
			payload: `{"event_id":"fedcba9876543210fedcba9876543210","event_type":"llm.response.completed","schema_version":"1.0","created_at":"` + now.Add(-29*time.Minute-time.Second).Format(time.RFC3339Nano) + `","installation_id":"install_test","run_id":"run_blocked","trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef","provider":"provider-a","requested_model":"model-a","input_tokens":7,"output_tokens":5,"cached_tokens":null,"usage_source":"provider","latency_ms":4,"status":"success","actual_cost":"0.003","estimated_cost":null,"content_capture":false}`,
		},
		{
			id: "0123456789abcdef0123456789abcdef", status: "policy_block", created: now.Add(-29 * time.Minute),
			payload: `{"event_id":"0123456789abcdef0123456789abcdef","event_type":"llm.policy.blocked","schema_version":"1.0","created_at":"` + now.Add(-29*time.Minute).Format(time.RFC3339Nano) + `","installation_id":"install_test","run_id":"run_blocked","trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef","provider":"provider-a","requested_model":"model-a","input_tokens":null,"output_tokens":null,"cached_tokens":null,"usage_source":"unknown","latency_ms":4,"status":"policy_block","policy_decision":{"decision":"block","reason":"request_limit_exceeded","policy":"max_requests_per_run","attempt":2,"threshold":1},"actual_cost":null,"estimated_cost":null,"content_capture":false}`,
		},
	}
	for _, event := range events {
		if _, err := db.Exec(`INSERT INTO events(event_id,installation_id,created_at_unix_ns,status,payload) VALUES(?,?,?,?,?)`, event.id, "install_test", event.created.UnixNano(), event.status, event.payload); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func timePointer(value time.Time) *time.Time { return &value }

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
