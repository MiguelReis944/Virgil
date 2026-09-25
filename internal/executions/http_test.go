package executions_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/controlauth"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func controlFixture(t *testing.T) (*httptest.Server, *executions.Registry, *storage.Journal, string) {
	return controlFixtureWithStore(t, nil)
}

func controlFixtureWithStore(t *testing.T, storeFactory func(*storage.Journal) executions.HTTPStore) (*httptest.Server, *executions.Registry, *storage.Journal, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close(); db.Close() })
	credential, err := controlauth.LoadOrCreate(filepath.Join(dir, "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	registry := executions.NewRegistry(j)
	var store executions.HTTPStore = j
	if storeFactory != nil {
		store = storeFactory(j)
	}
	h := executions.NewHTTPHandler(credential, registry, store)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/executions", h.Register)
	mux.HandleFunc("GET /api/executions/{run_id}/signals", h.Signals)
	mux.HandleFunc("POST /api/executions/{run_id}/running", h.Running)
	mux.HandleFunc("POST /api/executions/{run_id}/finish", h.Finish)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, registry, j, credential.Bearer()
}

type blockedBetweenReadAndFinish struct {
	*storage.Journal
	read   chan struct{}
	resume chan struct{}
	once   sync.Once
}

func (s *blockedBetweenReadAndFinish) Execution(ctx context.Context, runID string) (executions.Execution, error) {
	row, err := s.Journal.Execution(ctx, runID)
	s.once.Do(func() {
		close(s.read)
		<-s.resume
	})
	return row, err
}

func controlRequest(t *testing.T, method, url, bearer, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func registerRun(t *testing.T, server *httptest.Server, bearer, runID string) executions.Registration {
	t.Helper()
	res := controlRequest(t, http.MethodPost, server.URL+"/api/executions", bearer, `{"run_id":"`+runID+`"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", res.StatusCode)
	}
	var got executions.Registration
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.RunID != runID || got.RunToken == "" || got.SignalToken == "" || got.RunToken == got.SignalToken {
		t.Fatalf("invalid registration: %+v", got)
	}
	return got
}

func TestControlAuthenticationAndRegistration(t *testing.T) {
	server, _, _, bearer := controlFixture(t)
	for _, token := range []string{"", "wrong"} {
		res := controlRequest(t, http.MethodPost, server.URL+"/api/executions", token, `{}`)
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized || bytes.Contains(body, []byte(bearer)) {
			t.Fatalf("auth status=%d body=%q", res.StatusCode, body)
		}
	}
	registerRun(t, server, bearer, "run_a")
}

func TestControlRejectsPreviouslyUsedRunID(t *testing.T) {
	server, _, journal, bearer := controlFixture(t)
	if err := journal.StartExecution(context.Background(), "run_used", time.Now()); err != nil {
		t.Fatal(err)
	}
	res := controlRequest(t, http.MethodPost, server.URL+"/api/executions", bearer, `{"run_id":"run_used"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d, want %d", res.StatusCode, http.StatusConflict)
	}
}

func TestControlRejectsOversizedAndUnknownFields(t *testing.T) {
	server, _, _, bearer := controlFixture(t)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"unknown":1}`, http.StatusBadRequest},
		{`{"run_id":"` + strings.Repeat("a", 17*1024) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		res := controlRequest(t, http.MethodPost, server.URL+"/api/executions", bearer, tc.body)
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatalf("status = %d, want %d", res.StatusCode, tc.status)
		}
	}
}

func TestControlSignalsReadyBlockAndSingleClaim(t *testing.T) {
	server, registry, _, bearer := controlFixture(t)
	registration := registerRun(t, server, bearer, "run_signal")
	url := server.URL + "/api/executions/run_signal/signals"
	queryOnly := controlRequest(t, http.MethodGet, url+"?signal_token="+registration.SignalToken, bearer, "")
	queryOnly.Body.Close()
	if queryOnly.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token status = %d, want 401", queryOnly.StatusCode)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Virgil-Signal-Token", registration.SignalToken)
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("SSE headers: %d %v", res.StatusCode, res.Header)
	}
	reader := bufio.NewReader(res.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: ready\n" {
		t.Fatalf("first SSE line %q: %v", line, err)
	}
	second := controlRequest(t, http.MethodGet, url+"?signal_token="+registration.SignalToken, bearer, "")
	second.Body.Close()
	if second.StatusCode == http.StatusOK {
		t.Fatal("reused or query signal token accepted")
	}
	registry.NotifyPolicyBlock(executions.PolicyBlockNotice{RunID: "run_signal", Reason: "limit", Policy: "max_requests", Attempt: 2, Threshold: 1, OccurredAt: time.Now().UTC()})
	var event, data string
	for event == "" || data == "" {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	if event != "circuit_break" || strings.Contains(data, "\n") {
		t.Fatalf("event=%q data=%q", event, data)
	}
	var notice executions.PolicyBlockNotice
	if err := json.Unmarshal([]byte(data), &notice); err != nil || notice.RunID != "run_signal" || notice.Policy != "max_requests" {
		t.Fatalf("notice=%+v err=%v", notice, err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("signal stream did not close: %v", err)
	}
}

func TestControlSignalStreamClosesAfterTerminalFinish(t *testing.T) {
	server, _, _, bearer := controlFixture(t)
	registration := registerRun(t, server, bearer, "run_terminal")
	base := server.URL + "/api/executions/run_terminal"
	res := controlRequest(t, http.MethodPost, base+"/running", bearer, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("running status=%d", res.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/signals", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-Virgil-Signal-Token", registration.SignalToken)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if line, err := bufio.NewReader(stream.Body).ReadString('\n'); err != nil || line != "event: ready\n" {
		t.Fatalf("ready line=%q err=%v", line, err)
	}
	finished := controlRequest(t, http.MethodPost, base+"/finish", bearer, `{"state":"completed","exit_code":0}`)
	finished.Body.Close()
	if finished.StatusCode != http.StatusOK {
		t.Fatalf("finish status=%d", finished.StatusCode)
	}
	remaining, err := io.ReadAll(stream.Body)
	if err != nil || strings.Contains(string(remaining), "circuit_break") {
		t.Fatalf("terminal stream body=%q err=%v", remaining, err)
	}
}

func TestControlRejectsRunnerBlockedStateAndMalformedFinish(t *testing.T) {
	server, _, journal, bearer := controlFixture(t)
	registerRun(t, server, bearer, "run_invalid")
	url := server.URL + "/api/executions/run_invalid/finish"
	for _, body := range []string{
		`{"state":"blocked","exit_code":1}`,
		`{"state":"completed","unknown":1}`,
		`{"state":"failed","termination_status":"unknown"}`,
		`{"state":"termination_failed","termination_status":"failed"}`,
	} {
		res := controlRequest(t, http.MethodPost, url, bearer, body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, res.StatusCode)
		}
	}
	stored, err := journal.Execution(context.Background(), "run_invalid")
	if err != nil || stored.State != executions.StateStarting {
		t.Fatalf("invalid finish mutated row: %+v, %v", stored, err)
	}
}

func TestControlRunningAndFinishPersistSummary(t *testing.T) {
	server, _, journal, bearer := controlFixture(t)
	registerRun(t, server, bearer, "run_done")
	base := server.URL + "/api/executions/run_done"
	res := controlRequest(t, http.MethodPost, base+"/running", bearer, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("running status=%d", res.StatusCode)
	}
	res = controlRequest(t, http.MethodPost, base+"/finish", bearer, `{"state":"completed","exit_code":0}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("finish status=%d", res.StatusCode)
	}
	var summary struct {
		RunID    string `json:"run_id"`
		State    string `json:"state"`
		RunToken string `json:"run_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.RunID != "run_done" || summary.State != "completed" || summary.RunToken != "" {
		t.Fatalf("unsafe summary: %+v", summary)
	}
	stored, err := journal.Execution(context.Background(), "run_done")
	if err != nil || stored.State != executions.StateCompleted {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestControlBlockedFollowUpPreservesPolicyAndRecordsTerminationOnce(t *testing.T) {
	server, _, journal, bearer := controlFixture(t)
	registerRun(t, server, bearer, "run_blocked")
	base := server.URL + "/api/executions/run_blocked"
	res := controlRequest(t, http.MethodPost, base+"/running", bearer, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("running status=%d", res.StatusCode)
	}
	if err := journal.BlockExecution(context.Background(), executions.PolicyBlockNotice{RunID: "run_blocked", Policy: "max_requests", Reason: "limit", Attempt: 2, Threshold: 1, OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	res = controlRequest(t, http.MethodPost, base+"/finish", bearer, `{"state":"failed","exit_code":137,"termination_status":"succeeded"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("blocked finish status=%d", res.StatusCode)
	}
	stored, err := journal.Execution(context.Background(), "run_blocked")
	if err != nil || stored.State != executions.StateBlocked || stored.Policy != "max_requests" || stored.TerminationStatus != executions.TerminationSucceeded {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	res = controlRequest(t, http.MethodPost, base+"/finish", bearer, `{"state":"failed","termination_status":"failed","termination_error_code":"kill_failed"}`)
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Fatal("second termination report accepted")
	}
}

func TestControlFinishRetriesPolicyBlockBetweenReadAndWrite(t *testing.T) {
	store := &blockedBetweenReadAndFinish{read: make(chan struct{}), resume: make(chan struct{})}
	server, _, journal, bearer := controlFixtureWithStore(t, func(j *storage.Journal) executions.HTTPStore {
		store.Journal = j
		return store
	})
	registerRun(t, server, bearer, "run_race")
	base := server.URL + "/api/executions/run_race"
	running := controlRequest(t, http.MethodPost, base+"/running", bearer, `{}`)
	running.Body.Close()
	if running.StatusCode != http.StatusNoContent {
		t.Fatalf("running status=%d", running.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/finish", strings.NewReader(`{"state":"failed","exit_code":137,"termination_status":"succeeded"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	result := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			result <- err
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			result <- fmt.Errorf("finish status=%d", response.StatusCode)
			return
		}
		result <- nil
	}()
	select {
	case <-store.read:
	case <-ctx.Done():
		t.Fatal("finish did not read starting state")
	}
	defer func() {
		select {
		case <-store.resume:
		default:
			close(store.resume)
		}
	}()
	if err := journal.BlockExecution(context.Background(), executions.PolicyBlockNotice{RunID: "run_race", Policy: "max_requests", Reason: "limit", Attempt: 2, Threshold: 1, OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	close(store.resume)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("finish did not complete")
	}
	stored, err := journal.Execution(context.Background(), "run_race")
	if err != nil || stored.State != executions.StateBlocked || stored.TerminationStatus != executions.TerminationSucceeded || stored.Policy != "max_requests" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestControlFailedTerminationPreservesPolicy(t *testing.T) {
	server, _, journal, bearer := controlFixture(t)
	registerRun(t, server, bearer, "run_kill_failed")
	base := server.URL + "/api/executions/run_kill_failed"
	res := controlRequest(t, http.MethodPost, base+"/running", bearer, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("running status=%d", res.StatusCode)
	}
	if err := journal.BlockExecution(context.Background(), executions.PolicyBlockNotice{RunID: "run_kill_failed", Policy: "max_requests", Reason: "limit", Attempt: 2, Threshold: 1, OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	res = controlRequest(t, http.MethodPost, base+"/finish", bearer, `{"state":"termination_failed","exit_code":137,"termination_status":"failed","termination_error_code":"kill_failed"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("finish status=%d", res.StatusCode)
	}
	stored, err := journal.Execution(context.Background(), "run_kill_failed")
	if err != nil || stored.State != executions.StateTerminationFailed || stored.Policy != "max_requests" || stored.TerminationStatus != executions.TerminationFailed || stored.TerminationErrorCode != "kill_failed" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
