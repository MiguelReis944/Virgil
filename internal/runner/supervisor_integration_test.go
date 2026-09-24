package runner

import (
	"context"
	"encoding/json"
	"errors"
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

type realCoreFixture struct {
	client      *ControlClient
	journal     *storage.Journal
	registry    *executions.Registry
	registered  chan executions.Registration
	handler     *executions.HTTPHandler
	runningHook func(http.ResponseWriter, *http.Request)
	signalHook  func(http.ResponseWriter, *http.Request)
	mu          sync.Mutex
}

func newRealCoreFixture(t *testing.T) *realCoreFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close(); db.Close() })
	credential, err := controlauth.LoadOrCreate(filepath.Join(dir, "control.token"))
	if err != nil {
		t.Fatal(err)
	}
	f := &realCoreFixture{journal: journal, registry: executions.NewRegistry(journal), registered: make(chan executions.Registration, 1)}
	f.handler = executions.NewHTTPHandler(credential, f.registry, journal)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/executions", func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.handler.Register(recorder, r)
		response := recorder.Result()
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		w.Write(recorder.Body.Bytes())
		if response.StatusCode == http.StatusCreated {
			var registration executions.Registration
			if err := json.Unmarshal(recorder.Body.Bytes(), &registration); err == nil {
				f.registered <- registration
			}
		}
	})
	mux.HandleFunc("GET /api/executions/{run_id}/signals", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hook := f.signalHook
		f.mu.Unlock()
		if hook != nil {
			hook(w, r)
			return
		}
		f.handler.Signals(w, r)
	})
	mux.HandleFunc("POST /api/executions/{run_id}/running", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hook := f.runningHook
		f.mu.Unlock()
		if hook != nil {
			hook(w, r)
			return
		}
		f.handler.Running(w, r)
	})
	mux.HandleFunc("POST /api/executions/{run_id}/finish", f.handler.Finish)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	f.client, err = NewControlClient(server.URL, credential.Bearer())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *realCoreFixture) setHooks(running, signal func(http.ResponseWriter, *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runningHook = running
	f.signalHook = signal
}

func (f *realCoreFixture) assertFinishedAndRevoked(t *testing.T, state executions.State) executions.Execution {
	t.Helper()
	stored, err := f.journal.Execution(context.Background(), "run_real")
	if err != nil || stored.State != state || stored.EndedAt == nil {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	select {
	case registration := <-f.registered:
		if _, ok := f.registry.ResolveRunToken(registration.RunToken); ok {
			t.Fatal("run token remains active")
		}
	default:
		t.Fatal("registration was not captured")
	}
	return stored
}

func runRealFixture(t *testing.T, f *realCoreFixture, ctx context.Context, deadline time.Duration, process *supervisedProcess) (ExecutionSummary, error) {
	t.Helper()
	return superviseWithStarter(ctx, SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, RunID: "run_real", Deadline: deadline}, Control: f.client}, func(context.Context, RunSpec) (Process, error) { return process, nil })
}

func TestSupervisorStartupSignalFailureRevokesIdentity(t *testing.T) {
	f := newRealCoreFixture(t)
	f.setHooks(nil, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	started := false
	_, err := superviseWithStarter(context.Background(), SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, RunID: "run_real"}, Control: f.client}, func(context.Context, RunSpec) (Process, error) { started = true; return nil, nil })
	if err == nil || started {
		t.Fatalf("error=%v started=%v", err, started)
	}
	f.assertFinishedAndRevoked(t, executions.StateFailed)
}

func TestSupervisorMarkRunningFailureRevokesIdentity(t *testing.T) {
	f := newRealCoreFixture(t)
	f.setHooks(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, nil)
	p := &supervisedProcess{done: make(chan struct{}), exit: 1}
	_, err := runRealFixture(t, f, context.Background(), 0, p)
	if err == nil {
		t.Fatal("missing mark-running error")
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateFailed)
	if stored.TerminationStatus != executions.TerminationSucceeded {
		t.Fatalf("termination=%s", stored.TerminationStatus)
	}
}

func TestSupervisorStartingTerminationFailureRevokesIdentity(t *testing.T) {
	f := newRealCoreFixture(t)
	f.setHooks(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, nil)
	stopErr := errors.New("synthetic process tree failure")
	p := &supervisedProcess{done: make(chan struct{}), exit: 1, terminateErr: stopErr}
	_, err := runRealFixture(t, f, context.Background(), 0, p)
	if err == nil || !errors.Is(err, stopErr) || !strings.Contains(err.Error(), "mark execution running") {
		t.Fatalf("supervisor error=%v", err)
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateTerminationFailed)
	if stored.StopReason != "control_unavailable" || stored.TerminationStatus != executions.TerminationFailed || stored.TerminationErrorCode != "process_termination_failed" {
		t.Fatalf("stored=%+v", stored)
	}
}

func TestSupervisorPendingMarkTerminationFailurePreservesCause(t *testing.T) {
	for _, tc := range []struct {
		name, reason               string
		deadline                   time.Duration
		closeSignals, cancelParent bool
	}{
		{"deadline", "deadline", 70 * time.Millisecond, false, false},
		{"signal loss", "signal_lost", 0, true, false},
		{"cancellation", "interrupted", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRealCoreFixture(t)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			var signalHook func(http.ResponseWriter, *http.Request)
			if tc.closeSignals {
				signalHook = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Write([]byte("event: ready\n\n"))
					w.(http.Flusher).Flush()
				}
			}
			entered := make(chan struct{}, 1)
			f.setHooks(func(_ http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}, signalHook)
			stopErr := errors.New("synthetic process tree failure")
			p := &supervisedProcess{done: make(chan struct{}), exit: 1, terminateErr: stopErr}
			ctx := context.Background()
			if tc.cancelParent {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				go func() { <-entered; cancel() }()
			}
			_, err := runRealFixture(t, f, ctx, tc.deadline, p)
			if !errors.Is(err, stopErr) {
				t.Fatalf("supervisor error=%v", err)
			}
			stored := f.assertFinishedAndRevoked(t, executions.StateTerminationFailed)
			if stored.StopReason != tc.reason || stored.TerminationStatus != executions.TerminationFailed || stored.TerminationErrorCode != "process_termination_failed" {
				t.Fatalf("stored=%+v", stored)
			}
		})
	}
}

func TestSupervisorBlockedBeforeSSEDeliveryReconcilesNaturalExit(t *testing.T) {
	f := newRealCoreFixture(t)
	f.setHooks(func(w http.ResponseWriter, r *http.Request) {
		f.handler.Running(w, r)
		if err := f.journal.BlockExecution(context.Background(), executions.PolicyBlockNotice{RunID: "run_real", Policy: "max_requests", Reason: "limit", Attempt: 2, Threshold: 1, OccurredAt: time.Now().UTC()}); err != nil {
			t.Error(err)
		}
	}, nil)
	p := &supervisedProcess{done: make(chan struct{}), exit: 0}
	close(p.done)
	summary, err := runRealFixture(t, f, context.Background(), 0, p)
	if err != nil || summary.State != executions.StateBlocked {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateBlocked)
	if stored.TerminationStatus != executions.TerminationSucceeded {
		t.Fatalf("termination=%s", stored.TerminationStatus)
	}
}

func TestSupervisorDeadlineWhileMarkRunningStalls(t *testing.T) {
	f := newRealCoreFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.setHooks(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, nil)
	p := &supervisedProcess{done: make(chan struct{}), exit: 1}
	started := time.Now()
	_, err := runRealFixture(t, f, context.Background(), 80*time.Millisecond, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.terminatedAt.IsZero() || p.terminatedAt.Sub(started) > time.Second {
		t.Fatal("deadline did not stop the child while mark-running was pending")
	}
	select {
	case <-entered:
	default:
		t.Fatal("mark-running request was not entered")
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateFailed)
	if stored.StopReason != "deadline" {
		t.Fatalf("stop reason=%s", stored.StopReason)
	}
}

func TestSupervisorCancelWhileMarkRunningStalls(t *testing.T) {
	f := newRealCoreFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.setHooks(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-entered; cancel() }()
	p := &supervisedProcess{done: make(chan struct{}), exit: 1}
	_, err := runRealFixture(t, f, ctx, 0, p)
	if err != nil {
		t.Fatal(err)
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateFailed)
	if stored.StopReason != "interrupted" {
		t.Fatalf("stop reason=%s", stored.StopReason)
	}
}

func TestSupervisorSignalLossWhileMarkRunningStalls(t *testing.T) {
	f := newRealCoreFixture(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.setHooks(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: ready\n\n"))
		w.(http.Flusher).Flush()
	})
	p := &supervisedProcess{done: make(chan struct{}), exit: 1}
	started := time.Now()
	_, err := runRealFixture(t, f, context.Background(), 0, p)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("signal loss did not interrupt mark-running request")
	}
	stored := f.assertFinishedAndRevoked(t, executions.StateFailed)
	if stored.StopReason != "signal_lost" {
		t.Fatalf("stop reason=%s", stored.StopReason)
	}
}

func TestSupervisorNaturalNonzeroExitKeepsCode(t *testing.T) {
	f := newRealCoreFixture(t)
	p := &supervisedProcess{done: make(chan struct{}), exit: 7}
	close(p.done)
	summary, err := runRealFixture(t, f, context.Background(), 0, p)
	if err != nil || summary.ExitCode == nil || *summary.ExitCode != 7 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	f.assertFinishedAndRevoked(t, executions.StateFailed)
}
