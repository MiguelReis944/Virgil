package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/dashboard"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type EventRecorder interface {
	Record(ctx context.Context, event telemetry.Event) error
}

// PostflightRecorder is an optional extension of EventRecorder that can combine
// policy reconciliation, daily counter increment, and event append into one
// SQLite transaction.  *storage.Journal implements this interface.
type PostflightRecorder interface {
	EventRecorder
	PostflightAndAppend(ctx context.Context, outcome storage.PolicyOutcome, event telemetry.Event, destinations []string, costUSD string) error
}

// ContentSaver is an optional extension of EventRecorder for persisting
// raw prompt/response content alongside events. *storage.Journal implements this.
type ContentSaver interface {
	SaveContent(ctx context.Context, rec storage.ContentRecord) error
}

type Dependencies struct {
	DB                *sql.DB
	Client            *http.Client
	Getenv            func(string) string
	Recorder          EventRecorder
	InstallationID    string
	Policy            *policies.Engine
	ConfigPath        string // path to virgil.toml; enables GET/POST /setup when set
	DashboardPassword string // empty = no auth
}

func NewServer(cfg config.Config, deps Dependencies) (http.Handler, error) {
	if deps.DB == nil {
		return nil, errors.New("database connection is required")
	}
	if deps.Policy == nil && hasGuardrails(cfg.Guardrails) {
		return nil, errors.New("configured guardrails require a policy engine")
	}
	router, err := newRouter(cfg, deps.Client, deps.Getenv)
	if err != nil {
		return nil, err
	}
	router.recorder = deps.Recorder
	router.policy = deps.Policy
	router.installationID = deps.InstallationID
	if router.installationID == "" {
		id, err := telemetry.NewID(16)
		if err != nil {
			return nil, err
		}
		router.installationID = "install_" + id
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", router.chat)
	mux.HandleFunc("POST /v1/tool-results", router.toolResults)
	if deps.ConfigPath != "" {
		setupH := newSetupHandler(deps.ConfigPath)
		mux.HandleFunc("GET /setup", setupH)
		mux.HandleFunc("POST /setup", setupH)
	}
	dashLogin := dashboard.LoginHandler(deps.DashboardPassword)
	mux.HandleFunc("GET /login", dashLogin)
	mux.HandleFunc("POST /login", dashLogin)
	mux.HandleFunc("POST /logout", dashboard.LogoutHandler())
	mux.Handle("GET /dashboard", dashboard.DashboardHandler(deps.DB, deps.DashboardPassword))
	mux.Handle("GET /dashboard/run/{runID}", dashboard.RunHandler(deps.DB, deps.DashboardPassword))
	if cs, ok := deps.Recorder.(dashboard.ContentStore); ok {
		mux.Handle("GET /dashboard/event/{eventID}", dashboard.EventHandler(deps.DB, cs, deps.DashboardPassword))
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
		} else {
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		body := struct {
			Status  string `json:"status"`
			Storage string `json:"storage"`
		}{Status: "ready", Storage: "ready"}
		if err := deps.DB.PingContext(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
			body.Status = "unready"
			body.Storage = "unready"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux, nil
}

func hasGuardrails(g config.GuardrailsConfig) bool {
	return g.MaxRequestsPerRun != 0 || g.MaxCostPerRunUSD != "" || g.MaxInputTokensPerRun != 0 ||
		g.MaxOutputTokensPerRun != 0 || g.MaxTotalTokensPerRun != 0 || g.MaxDurationSeconds != 0 ||
		g.MaxToolCallsPerRun != 0 || len(g.AllowedProviders) != 0 || len(g.AllowedModels) != 0 || len(g.AllowedTools) != 0
}

func ListenAndServe(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return serve(ctx, listener, handler)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	srv := newHTTPServer(listener.Addr().String(), handler)
	srv.BaseContext = func(net.Listener) context.Context { return ctx }
	errs := make(chan error, 1)
	go func() {
		errs <- srv.Serve(listener)
	}()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := srv.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = srv.Close()
		}
		serveErr := <-errs
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return shutdownErr
	}
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}
