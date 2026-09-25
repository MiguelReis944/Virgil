package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

type supervisedProcess struct {
	done         chan struct{}
	once         sync.Once
	exit         int
	terminateErr error
	terminatedAt time.Time
}

func (p *supervisedProcess) PID() int            { return 42 }
func (p *supervisedProcess) Wait() ProcessResult { <-p.done; return ProcessResult{ExitCode: p.exit} }
func (p *supervisedProcess) Terminate(context.Context) error {
	p.once.Do(func() { p.terminatedAt = time.Now(); close(p.done) })
	return p.terminateErr
}

type supervisorFixture struct {
	server   *httptest.Server
	client   *ControlClient
	ready    chan struct{}
	signal   chan string
	running  chan struct{}
	finished chan executions.FinishRequest
}

func newSupervisorFixture(t *testing.T) *supervisorFixture {
	t.Helper()
	f := &supervisorFixture{ready: make(chan struct{}), signal: make(chan string, 1), running: make(chan struct{}, 1), finished: make(chan executions.FinishRequest, 1)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-credential" {
			t.Errorf("missing control bearer: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/executions":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"run_id":"run_fixture","run_token":"run-secret","signal_token":"signal-secret"}`)
		case "/api/executions/run_fixture/signals":
			if r.Header.Get("X-Virgil-Signal-Token") != "signal-secret" {
				t.Error("missing signal token")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			<-f.ready
			fmt.Fprint(w, "event: ready\n\n")
			w.(http.Flusher).Flush()
			select {
			case event := <-f.signal:
				fmt.Fprint(w, event)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
			}
		case "/api/executions/run_fixture/running":
			f.running <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		case "/api/executions/run_fixture/finish":
			var result executions.FinishRequest
			if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
				t.Error(err)
			}
			f.finished <- result
			state := result.State
			if result.StopReason == "policy_block" && result.TerminationStatus == executions.TerminationSucceeded {
				state = executions.StateBlocked
			}
			fmt.Fprintf(w, `{"run_id":"run_fixture","state":%q,"exit_code":%d,"termination_status":%q}`, state, result.ExitCode, result.TerminationStatus)
		default:
			t.Errorf("unexpected control path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(f.server.Close)
	var err error
	f.client, err = NewControlClient(f.server.URL, "local-credential")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSupervisorWaitsForReadyBeforeStarting(t *testing.T) {
	f := newSupervisorFixture(t)
	started := make(chan struct{}, 1)
	p := &supervisedProcess{done: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := superviseWithStarter(context.Background(), SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, RunID: "run_fixture"}, Control: f.client}, func(context.Context, RunSpec) (Process, error) { started <- struct{}{}; return p, nil })
		done <- err
	}()
	select {
	case <-started:
		t.Fatal("child started before ready")
	case <-time.After(30 * time.Millisecond):
	}
	close(f.ready)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("child did not start after ready")
	}
	p.once.Do(func() { close(p.done) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not finish")
	}
	select {
	case finish := <-f.finished:
		if finish.State != executions.StateCompleted || finish.TerminationStatus != executions.TerminationNotRequired {
			t.Fatalf("finish=%+v", finish)
		}
	default:
		t.Fatal("missing finish")
	}
}

func TestSupervisorStopsOnCircuitBreakAndSignalLoss(t *testing.T) {
	for _, tc := range []struct {
		name, event string
		state       executions.State
		reason      string
	}{
		{"block", "event: circuit_break\ndata: {\"run_id\":\"run_fixture\",\"policy\":\"max_requests\"}\n\n", executions.StateBlocked, "policy_block"},
		{"disconnect", "", executions.StateFailed, "signal_lost"},
		{"wrong run", "event: circuit_break\ndata: {\"run_id\":\"other\"}\n\n", executions.StateFailed, "signal_lost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisorFixture(t)
			close(f.ready)
			p := &supervisedProcess{done: make(chan struct{}), exit: 1}
			f.signal <- tc.event
			summary, err := superviseWithStarter(context.Background(), SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, RunID: "run_fixture"}, Control: f.client}, func(context.Context, RunSpec) (Process, error) { return p, nil })
			if err != nil {
				t.Fatal(err)
			}
			if summary.State != tc.state && !(tc.reason == "signal_lost" && summary.State == executions.StateGatewayFailed) {
				t.Fatalf("state=%s", summary.State)
			}
			finish := <-f.finished
			if finish.StopReason != tc.reason || finish.TerminationStatus != executions.TerminationSucceeded {
				t.Fatalf("finish=%+v", finish)
			}
		})
	}
}

func TestSupervisorReportsStartAndTerminationFailures(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		startErr, terminateErr error
		wantState              executions.State
	}{
		{"start", errors.New("missing executable"), nil, executions.StateFailed},
		{"terminate", nil, errors.New("job failed"), executions.StateTerminationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSupervisorFixture(t)
			close(f.ready)
			p := &supervisedProcess{done: make(chan struct{}), terminateErr: tc.terminateErr}
			if tc.terminateErr != nil {
				f.signal <- "event: circuit_break\ndata: {\"run_id\":\"run_fixture\"}\n\n"
			}
			_, err := superviseWithStarter(context.Background(), SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, RunID: "run_fixture"}, Control: f.client}, func(context.Context, RunSpec) (Process, error) {
				if tc.startErr != nil {
					return nil, tc.startErr
				}
				return p, nil
			})
			if err == nil {
				t.Fatal("missing startup or termination error")
			}
			finish := <-f.finished
			if finish.State != tc.wantState {
				t.Fatalf("finish=%+v", finish)
			}
			if tc.terminateErr != nil && (finish.TerminationStatus != executions.TerminationFailed || finish.TerminationCode == "") {
				t.Fatalf("termination=%+v", finish)
			}
		})
	}
}

func TestSupervisorRejectsChildCredentialAndIdentityOverrides(t *testing.T) {
	client, err := NewControlClient("http://127.0.0.1:1", "credential")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"VIRGIL_RUN_ID", "OPENAI_BASE_URL", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"} {
		_, err := Supervise(context.Background(), SupervisionSpec{Run: RunSpec{Command: []string{"synthetic"}, Env: map[string]string{key: "untrusted"}}, Control: client})
		if err == nil || !strings.Contains(err.Error(), "child environment") {
			t.Errorf("accepted child override %q: %v", key, err)
		}
	}
}
