package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/settings"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestSetupRedirectsIntoProvidersPanel(t *testing.T) {
	rec := httptest.NewRecorder()
	NewSetupHandler("unused").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/providers?onboarding=1" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHealthReportsAppliedAndPendingConfigHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "virgil.toml")
	original := []byte("[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	applied, err := settings.HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dir, "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := NewServer(config.Config{}, Dependencies{DB: db, ConfigPath: path, AppliedConfigHash: applied})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(original, []byte("\n[privacy]\ncapture_prompts=true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Applied, Pending string
		Restart          bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &struct {
		Applied *string `json:"applied_config_hash"`
		Pending *string `json:"pending_config_hash"`
		Restart *bool   `json:"restart_required"`
	}{Applied: &body.Applied, Pending: &body.Pending, Restart: &body.Restart}); err != nil {
		t.Fatal(err)
	}
	if body.Applied != applied || body.Pending == "" || body.Pending == body.Applied || !body.Restart {
		t.Fatalf("health=%+v", body)
	}
}
