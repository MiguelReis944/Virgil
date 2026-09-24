package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
)

func TestSettingsHandlerShowsSafeReadOnlyConfiguration(t *testing.T) {
	store := testSettingsStore(t)
	login := httptest.NewRecorder()
	ok, err := Login(login, "configured-secret", "configured-secret")
	if err != nil || !ok {
		t.Fatalf("login: ok=%t err=%v", ok, err)
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing login cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/settings", nil)
	req.AddCookie(cookies[0])
	rec := httptest.NewRecorder()
	SettingsHandler(store, "configured-secret").ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"30 days", "Disabled", "Configured", "127.0.0.1:8787", "Read only"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, forbidden := range []string{"configured-secret", store.Path(), "LOCAL_KEY"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("settings leaked %q", forbidden)
		}
	}
}

func TestSettingsHandlerShowsAppliedBindAfterPendingChange(t *testing.T) {
	store := testSettingsStore(t)
	applied, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		cfg.Server.Listen = "127.0.0.1:9797"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SettingsHandler(store, "", applied).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/settings", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "127.0.0.1:8787") || strings.Contains(rec.Body.String(), "127.0.0.1:9797") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsHandlerReturnsGenericError(t *testing.T) {
	rec := httptest.NewRecorder()
	SettingsHandler(nil, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/settings", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "Settings are temporarily unavailable") || strings.Contains(got, "nil") {
		t.Fatalf("body=%q", got)
	}
}
