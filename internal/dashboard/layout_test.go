package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestRenderPageProvidesProductNavigationAndSecurityHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	err := renderPage(rec, pageData{Title: "Overview", ActiveSection: sectionOverview}, overviewBody, overviewData{})
	if err != nil {
		t.Fatal(err)
	}

	body := rec.Body.String()
	for _, want := range []string{"Overview", "Executions", "Protections", "Providers", "Usage", "Health", "Settings"} {
		if !strings.Contains(body, ">"+want+"<") {
			t.Errorf("navigation does not contain %q", want)
		}
	}
	if !strings.Contains(body, `href="/dashboard" aria-current="page"`) {
		t.Fatalf("overview navigation is not active: %s", body)
	}
	for _, want := range []string{
		`<html lang="en" data-theme="golden-bough">`,
		`class="skip-link" href="#main-content"`,
		`class="brand-mark"`,
		`class="nav-status"`,
		`id="main-content"`,
		`--accent:#c6a15b`,
		`<h2>Execution safety</h2>`,
		`class="metric-grid operational-grid"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shared shell does not contain %q", want)
		}
	}
	assertPanelSecurityHeaders(t, rec.Header())
	_ = req
}

func TestRequirePanelAuthRedirectsUnauthenticatedRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	called := false

	requirePanelAuth("secret", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})).ServeHTTP(rec, req)

	if called {
		t.Fatal("protected handler was called")
	}
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func assertPanelSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"Content-Security-Policy": "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
}

type emptyContentStore struct{}

func (emptyContentStore) GetContent(context.Context, string) (storage.ContentRecord, error) {
	return storage.ContentRecord{}, errors.New("not found")
}

func TestAuthenticatedPanelHandlersUseOneShellContract(t *testing.T) {
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

	tests := []struct {
		name    string
		path    string
		active  string
		handler http.Handler
		pathKey string
		pathVal string
	}{
		{"overview", "/dashboard", "/dashboard", OverviewHandler(db, ""), "", ""},
		{"usage", "/dashboard/usage", "/dashboard/usage", DashboardHandler(db, ""), "", ""},
		{"session", "/dashboard/session/1", "/dashboard/usage", SessionHandler(db, ""), "bucketTS", "1"},
		{"run", "/dashboard/run/run-test", "/dashboard/executions", RunHandler(db, ""), "runID", "run-test"},
		{"event", "/dashboard/event/event-test", "/dashboard/usage", EventHandler(db, emptyContentStore{}, ""), "eventID", "event-test"},
		{"executions placeholder", "/dashboard/executions", "/dashboard/executions", PlaceholderHandler("", "Executions", "executions"), "", ""},
		{"protections placeholder", "/dashboard/protections", "/dashboard/protections", PlaceholderHandler("", "Protections", "protections"), "", ""},
		{"providers placeholder", "/dashboard/providers", "/dashboard/providers", PlaceholderHandler("", "Providers", "providers"), "", ""},
		{"health placeholder", "/dashboard/health", "/dashboard/health", PlaceholderHandler("", "Health", "health"), "", ""},
		{"settings placeholder", "/dashboard/settings", "/dashboard/settings", PlaceholderHandler("", "Settings", "settings"), "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.pathKey != "" {
				req.SetPathValue(tc.pathKey, tc.pathVal)
			}
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Count(strings.ToLower(body), "<!doctype html>") != 1 {
				t.Fatalf("expected exactly one document shell")
			}
			for _, label := range []string{"Overview", "Executions", "Protections", "Providers", "Usage", "Health", "Settings"} {
				if !strings.Contains(body, ">"+label+"<") {
					t.Errorf("missing navigation label %q", label)
				}
			}
			if !strings.Contains(body, `href="`+tc.active+`" aria-current="page"`) {
				t.Errorf("active navigation missing for %s", tc.active)
			}
			assertPanelSecurityHeaders(t, rec.Header())
		})
	}
}

func TestUsageHandlerProvidesActionableEmptyAndGenericErrorStates(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	DashboardHandler(db, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/usage", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Run an application through Virgil") {
		t.Fatalf("empty state status=%d body=%s", rec.Code, rec.Body.String())
	}
	journal.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	DashboardHandler(db, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/usage", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Usage information is temporarily unavailable") {
		t.Fatalf("error state status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "database") {
		t.Fatalf("error state exposed internal details: %s", rec.Body.String())
	}
}

func TestPanelHandlersRedirectWhenAuthenticationIsRequired(t *testing.T) {
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

	for _, handler := range []http.Handler{
		OverviewHandler(db, "secret"), DashboardHandler(db, "secret"), SessionHandler(db, "secret"),
		RunHandler(db, "secret"), EventHandler(db, emptyContentStore{}, "secret"), PlaceholderHandler("secret", "Health", "health"),
	} {
		req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
			t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		assertPanelSecurityHeaders(t, rec.Header())
	}
}

func TestOverviewDatabaseErrorKeepsSecurityHeaders(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	_ = db.Close()

	rec := httptest.NewRecorder()
	OverviewHandler(db, "").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertPanelSecurityHeaders(t, rec.Header())
}
