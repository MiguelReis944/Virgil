package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
)

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
