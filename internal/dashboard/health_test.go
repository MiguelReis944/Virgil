package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestHealthHandlerShowsOperationalStateWithoutSensitiveValues(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	if _, err := db.Exec(`INSERT INTO executions(run_id,state,started_at_unix_ns) VALUES ('run_active','running',?)`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	store := testSettingsStore(t)
	applied, err := settings.HashFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	HealthHandler(store, applied, "", HealthOptions{DB: db, StartedAt: time.Now().Add(-90 * time.Minute), Version: "1.2.3", AppliedProviders: 1}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/health", nil))
	body := rec.Body.String()
	for _, want := range []string{"Ready", "1h 30m", "SQLite", "1", "Active runs", "Dead letters", "1.2.3"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	for _, forbidden := range []string{store.Path(), "LOCAL_KEY", "virgil.db"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("health leaked %q", forbidden)
		}
	}
}

func TestHealthHandlerShowsPendingRestartWithoutPathsOrSecrets(t *testing.T) {
	store := testSettingsStore(t)
	applied, err := settings.HashFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(cfg *config.Config) error { cfg.Guardrails.MaxRequestsPerRun = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	HealthHandler(store, applied, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/health", nil))
	body := rec.Body.String()
	for _, want := range []string{"Health", "Restart required", applied[:12]} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, store.Path()) || strings.Contains(body, "LOCAL_KEY") {
		t.Fatal("health leaked path or credential reference")
	}
}

func TestHealthHandlerShowsUpToDate(t *testing.T) {
	store := testSettingsStore(t)
	applied, _ := settings.HashFile(store.Path())
	rec := httptest.NewRecorder()
	HealthHandler(store, applied, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/health", nil))
	if !strings.Contains(rec.Body.String(), "Up to date") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHealthHandlerShowsAppliedProviderCountAfterPendingChange(t *testing.T) {
	store := testSettingsStore(t)
	applied, err := settings.HashFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		cfg.Providers = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	HealthHandler(store, applied, "", HealthOptions{AppliedProviders: 1}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Restart required") || !strings.Contains(rec.Body.String(), "Available from the local configuration") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
