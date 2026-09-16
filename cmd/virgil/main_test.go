package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestLocalGatewayWiresDurableJournal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_fixture","object":"chat.completion","model":"fixture-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"fixture": {Type: "openai-compatible", BaseURL: upstream.URL + "/v1", Model: "fixture-model"},
	}}
	handler, journal, err := buildHandler(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fixture-model","messages":[{"role":"user","content":"synthetic"}]}`))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("persisted events=%d", count)
	}
}
