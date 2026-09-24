package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func configuredTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config = newHTTPServer("", handler)
	if server.Config.ReadTimeout <= 0 {
		t.Fatal("server must bound request body reads")
	}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestSlowChatBodyTimesOut(t *testing.T) {
	handler := chatServer(t, "http://127.0.0.1:1", func(string) string { return "" })
	server := httptest.NewUnstartedServer(handler)
	server.Config = newHTTPServer("", handler)
	server.Config.ReadTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: fixture\r\nContent-Length: 100\r\nAuthorization: Bearer synthetic-key\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want 408", response.StatusCode)
	}
}

func TestConfiguredServerRejectsOversizedChatBody(t *testing.T) {
	handler := chatServer(t, "http://127.0.0.1:1", func(string) string { return "" })
	server := configuredTestServer(t, handler)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(strings.Repeat("x", maxChatRequest+1)))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.StatusCode)
	}
}

func TestConfiguredServerForwardsNormalChatJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"fixture-model","choices":[]}`)
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	server := configuredTestServer(t, handler)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", bytes.NewReader(chatFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer synthetic-key")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

func TestShutdownWithoutActiveConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	}()
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop without active connections")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("listener remains open after server goroutine exits")
	}
}

func TestShutdownCancelsActiveRequestAndJoinsServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	requestCancelled := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(requestCancelled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- serve(ctx, listener, handler) }()
	clientFinished := make(chan struct{})
	go func() {
		defer close(clientFinished)
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case <-requestCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("active request context was not cancelled")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not terminate")
	}
	select {
	case <-clientFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("client request did not terminate")
	}
}

func TestShutdownCancelsUpstreamStream(t *testing.T) {
	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"model\":\"fixture-model\",\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	handler := chatServer(t, upstream.URL, func(string) string { return "" })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- serve(ctx, listener, handler) }()
	req, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String()+"/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer synthetic-key")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream stream continued after shutdown")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish after stream cancellation")
	}
}

func TestShutdownReturnsAtDeadlineForStuckHandler(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- serve(ctx, listener, handler) }()
	clientFinished := make(chan struct{})
	go func() {
		defer close(clientFinished)
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v, want deadline exceeded", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("shutdown exceeded its deadline")
	}
	close(release)
	released = true
	select {
	case <-clientFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("client request did not terminate")
	}
}

func TestListenAndServeReturnsImmediateListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ListenAndServe(ctx, listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})); err == nil {
		t.Fatal("expected listen error for occupied address")
	}
}

func TestHealthReportsSQLiteReadinessWithoutSecrets(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler, err := NewServer(config.Config{}, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status  string `json:"status"`
		Storage string `json:"storage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ready" || body.Storage != "ready" {
		t.Fatalf("health = %+v", body)
	}
	if rec.Body.String() == "" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatal("health response is not JSON")
	}
}

func TestHealthUnreadyWhenSQLiteClosed(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServer(config.Config{}, Dependencies{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPanelRoutesUseUnifiedNavigationAndSecurityContract(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		journal.Close()
		_ = db.Close()
	}()
	handler, err := NewServer(config.Config{}, Dependencies{DB: db, Recorder: journal})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/dashboard", "/dashboard/usage", "/dashboard/session/1",
		"/dashboard/event/event-test", "/dashboard/executions", "/dashboard/protections",
		"/dashboard/providers", "/dashboard/health", "/dashboard/settings",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, label := range []string{"Overview", "Executions", "Protections", "Providers", "Usage", "Health", "Settings"} {
				if !strings.Contains(rec.Body.String(), ">"+label+"<") {
					t.Errorf("%s missing %s navigation", path, label)
				}
			}
			for name := range map[string]struct{}{
				"Content-Security-Policy": {}, "X-Frame-Options": {},
				"X-Content-Type-Options": {}, "Referrer-Policy": {},
			} {
				if rec.Header().Get(name) == "" {
					t.Errorf("%s missing %s", path, name)
				}
			}
		})
	}
}

func TestLegacyRunRouteRedirectsToExecutionDetail(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		journal.Close()
		_ = db.Close()
	}()
	handler, err := NewServer(config.Config{}, Dependencies{DB: db, Recorder: journal})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/run/run-test", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/dashboard/executions/run-test" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}
