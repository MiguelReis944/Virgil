package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

func TestControlClientRejectsNonLoopbackAndRedirects(t *testing.T) {
	for _, address := range []string{"http://example.com:8787", "https://127.0.0.1:8787", "http://localhost:8787", "http://127.0.0.1:8787/path", "http://127.0.0.1:8787/?token=secret"} {
		if _, err := NewControlClient(address, "credential"); err == nil {
			t.Errorf("accepted address %q", address)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/steal", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := NewControlClient(server.URL, "credential")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(context.Background(), "run_redirect"); err == nil {
		t.Fatal("registration followed redirect")
	}
}

func TestControlClientRegistersAndFinishesWithBearer(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer credential" {
			t.Errorf("missing control bearer on %s", r.URL.Path)
		}
		requests++
		switch r.URL.Path {
		case "/api/executions":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"run_id":"run_fixture","run_token":"run-secret","signal_token":"signal-secret"}`)
		case "/api/executions/run_fixture/running":
			w.WriteHeader(http.StatusNoContent)
		case "/api/executions/run_fixture/finish":
			fmt.Fprint(w, `{"run_id":"run_fixture","state":"completed","exit_code":0}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewControlClient(server.URL, "credential")
	if err != nil {
		t.Fatal(err)
	}
	registration, err := client.Register(context.Background(), "run_fixture")
	if err != nil || registration.RunToken != "run-secret" || registration.SignalToken != "signal-secret" {
		t.Fatalf("register = %+v, %v", registration, err)
	}
	if err := client.MarkRunning(context.Background(), registration.RunID); err != nil {
		t.Fatal(err)
	}
	summary, err := client.Finish(context.Background(), registration.RunID, executions.FinishRequest{State: executions.StateCompleted, TerminationStatus: executions.TerminationNotRequired})
	if err != nil || summary.RunID != "run_fixture" || summary.State != executions.StateCompleted || summary.ExitCode == nil || *summary.ExitCode != 0 {
		t.Fatalf("finish = %+v, %v", summary, err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestControlClientRequiresReadyAndBoundsResponses(t *testing.T) {
	for _, body := range []string{`{"run_id":"run_fixture"}`, strings.Repeat("x", 65<<10)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, body)
		}))
		client, err := NewControlClient(server.URL, "credential")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Register(context.Background(), "run_fixture"); err == nil {
			t.Fatal("accepted invalid registration")
		}
		server.Close()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: circuit_break\n\n")
	}))
	defer server.Close()
	client, _ := NewControlClient(server.URL, "credential")
	if _, _, err := client.Signals(context.Background(), executions.Registration{RunID: "run_fixture", SignalToken: "signal-secret"}); err == nil {
		t.Fatal("accepted signal stream without ready")
	}
}

func TestControlClientDecodesSignalAndConnectionLoss(t *testing.T) {
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer credential" || r.Header.Get("X-Virgil-Signal-Token") != "signal-secret" {
			t.Error("missing signal authentication")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": heartbeat\n\nevent: ready\n\n")
		w.(http.Flusher).Flush()
		<-ready
		fmt.Fprint(w, "event: circuit_break\ndata: {\"run_id\":\"run_fixture\",\"policy\":\"max_requests\"}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client, _ := NewControlClient(server.URL, "credential")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals, failures, err := client.Signals(ctx, executions.Registration{RunID: "run_fixture", SignalToken: "signal-secret"})
	if err != nil {
		t.Fatal(err)
	}
	close(ready)
	select {
	case signal := <-signals:
		if signal.Kind != executions.SignalCircuitBreak || signal.PolicyBlock.Policy != "max_requests" {
			t.Fatalf("signal = %+v", signal)
		}
	case err := <-failures:
		t.Fatalf("signal error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("missing circuit break")
	}
}
