package dashboard

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestProtectionsHandlerRendersEveryLocalGuardrail(t *testing.T) {
	h := ProtectionsHandler(testSettingsStore(t), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/protections", nil))
	for _, want := range []string{"Calls per run", "Input tokens per run", "Output tokens per run", "Total tokens per run", "Cost per run", "Duration", "Tool calls", "Allowed providers", "Allowed models", "Allowed tools", "Daily calls", "Daily cost", "Repetition rule", "csrf_token"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestProtectionsHandlerInvalidValueDoesNotWrite(t *testing.T) {
	store := testSettingsStore(t)
	before, _ := store.Load()
	h := ProtectionsHandler(store, "")
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/dashboard/protections", nil))
	token := extractCSRF(t, get.Body.String())
	body := "csrf_token=" + token + "&max_requests_per_run=-1"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/dashboard/protections", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cannot be negative") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	after, _ := store.Load()
	if !reflect.DeepEqual(after.Guardrails, before.Guardrails) {
		t.Fatal("invalid update changed config")
	}
}

func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="csrf_token" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("csrf token missing")
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatal("csrf token malformed")
	}
	return rest[:j]
}
