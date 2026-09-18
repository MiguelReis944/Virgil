package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestControlPlanePolicySyncAndRestart(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	engine, err := policies.NewEngine(journal, policies.Limits{MaxCallsPerRun: 5})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/policies/current" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("ETag", `"v1"`)
		_ = json.NewEncoder(w).Encode(controlplane.PolicyEnvelope{Version: 1, Limits: controlplane.CPLimits{MaxCallsPerRun: 1}})
	}))
	defer server.Close()
	client := controlplane.NewClient(server.URL, "fixture", nil)
	var etag string
	if err := syncControlPlanePolicy(ctx, engine, client, &etag); err != nil {
		t.Fatal(err)
	}
	if etag != `"v1"` || engine.RemoteVersion() != 1 {
		t.Fatalf("etag=%s version=%d", etag, engine.RemoteVersion())
	}
	restarted, err := policies.NewEngine(journal, policies.Limits{MaxCallsPerRun: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCachedControlPlanePolicy(ctx, journal, restarted); err != nil {
		t.Fatal(err)
	}
	first, err := restarted.Preflight(ctx, policies.RequestFacts{RunID: "run_cached", Provider: "p", Model: "m"})
	if err != nil || first.Decision != "allow" {
		t.Fatalf("first=%+v %v", first, err)
	}
	second, err := restarted.Preflight(ctx, policies.RequestFacts{RunID: "run_cached", Provider: "p", Model: "m"})
	if err != nil || second.Reason != "call_limit" {
		t.Fatalf("second=%+v %v", second, err)
	}
}
