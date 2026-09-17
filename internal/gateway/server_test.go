package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
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
