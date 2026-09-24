package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/controlauth"
	"github.com/MiguelReis944/Virgil/internal/dashboard"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/gateway"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/settings"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type CoreOptions struct {
	ConfigPath        string
	EnvPath           string
	OpenPanel         bool
	StartControlPlane func(context.Context, config.Config, *storage.Journal, *policies.Engine) (func(), error)
}

func RunCore(ctx context.Context, options CoreOptions) error {
	configPath := options.ConfigPath
	if configPath == "" {
		configPath = "virgil.toml"
	}
	envPath := options.EnvPath
	if envPath == "" {
		envPath = ".env"
	}
	loadDotEnv(envPath)
	cfg, err := config.Load(configPath, os.Getenv)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load config %q: %w", configPath, err)
		}
		slog.Warn("config missing; starting setup server", "reason", err, "url", "http://127.0.0.1:8787/setup")
		return runSetupServer(ctx, configPath, options.OpenPanel)
	}
	credential, err := controlauth.LoadOrCreate(filepath.Join(filepath.Dir(cfg.Storage.Path), "control.token"))
	if err != nil {
		return fmt.Errorf("load control credential: %w", err)
	}
	level := slog.LevelInfo
	switch cfg.Server.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	db, err := storage.Open(cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	readDB, err := storage.OpenReadPool(cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer readDB.Close()
	handler, journal, engine, err := buildHandlerWithControl(cfg, configPath, db, readDB, &credential)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.RecoverExecutions(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover executions: %w", err)
	}
	if cfg.Storage.RetentionDays > 0 {
		if _, err := journal.Prune(context.Background(), time.Now().AddDate(0, 0, -cfg.Storage.RetentionDays)); err != nil {
			slog.Warn("retention prune failed; continuing", "error", err)
		}
	}
	if cfg.ControlPlane.Enabled {
		if options.StartControlPlane == nil {
			return fmt.Errorf("control plane startup is unavailable")
		}
		stop, err := options.StartControlPlane(ctx, cfg, journal, engine)
		if err != nil {
			return err
		}
		defer stop()
	}
	if cfg.Dashboard.Password == "" {
		slog.Warn("dashboard has no password — accessible to anyone on this machine; set [dashboard] password in virgil.toml")
	}
	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Server.Listen, err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	slog.Info("gateway listening", "address", listener.Addr().String(), "panel", panelURL)
	var open func(string) error
	if options.OpenPanel {
		open = OpenBrowser
	}
	return servePanel(ctx, listener, handler, panelURL, open)
}

func runSetupServer(ctx context.Context, configPath string, openPanel bool) error {
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil && filepath.Dir(configPath) != "." {
			return fmt.Errorf("create config directory: %w", err)
		}
		defaultConfig := []byte("[server]\nlisten = \"127.0.0.1:8787\"\nlog_level = \"info\"\n\n[storage]\npath = \"./data/virgil.db\"\nretention_days = 30\n")
		if err := os.WriteFile(configPath, defaultConfig, 0o600); err != nil {
			return fmt.Errorf("create initial config: %w", err)
		}
	}
	mux := http.NewServeMux()
	setupH := gateway.NewSetupHandler(configPath)
	mux.HandleFunc("GET /setup", setupH)
	mux.HandleFunc("POST /setup", setupH)
	settingsStore := settings.New(configPath)
	providersHandler := dashboard.ProvidersHandler(settingsStore, "")
	protectionsHandler := dashboard.ProtectionsHandler(settingsStore, "")
	mux.Handle("GET /dashboard/providers", providersHandler)
	mux.Handle("POST /dashboard/providers", providersHandler)
	mux.Handle("GET /dashboard/protections", protectionsHandler)
	mux.Handle("POST /dashboard/protections", protectionsHandler)
	mux.HandleFunc("GET /dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/providers?onboarding=1", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dashboard/providers?onboarding=1", http.StatusFound)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		return fmt.Errorf("listen setup server: %w", err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	slog.Info("setup server listening", "address", listener.Addr().String(), "panel", panelURL)
	var open func(string) error
	if openPanel {
		open = OpenBrowser
	}
	return servePanel(ctx, listener, mux, panelURL, open)
}

// servePanel opens the browser only after the panel handler has answered a request.
func servePanel(ctx context.Context, listener net.Listener, handler http.Handler, panelURL string, open func(string) error) error {
	if open == nil {
		return gateway.Serve(ctx, listener, handler)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = gateway.Serve(serveCtx, listener, handler)
		close(done)
	}()
	readyCtx, stopWaiting := context.WithTimeout(ctx, 5*time.Second)
	defer stopWaiting()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for {
		request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, panelURL, nil)
		if err != nil {
			cancel()
			<-done
			return fmt.Errorf("panel readiness request: %w", err)
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest {
				break
			}
		}
		if ctx.Err() != nil {
			cancel()
			<-done
			return serveErr
		}
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return serveErr
		case <-done:
			return serveErr
		case <-readyCtx.Done():
			cancel()
			<-done
			return fmt.Errorf("panel did not become ready: %w", readyCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		cancel()
		<-done
		return serveErr
	}
	select {
	case <-done:
		return serveErr
	default:
	}
	if err := open(panelURL); err != nil {
		slog.Warn("could not open panel", "error", err, "url", panelURL)
	}
	<-done
	return serveErr
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), "\"'")
		if k != "" && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	slog.Info("loaded .env", "path", path)
}

func BuildHandlerWithEngine(cfg config.Config, configPath string, db, readDB *sql.DB) (http.Handler, *storage.Journal, *policies.Engine, error) {
	return buildHandlerWithControl(cfg, configPath, db, readDB, nil)
}

func buildHandlerWithControl(cfg config.Config, configPath string, db, readDB *sql.DB, credential *controlauth.Credential) (http.Handler, *storage.Journal, *policies.Engine, error) {
	journal, err := storage.NewJournalWithReadPool(db, readDB)
	if err != nil {
		return nil, nil, nil, err
	}
	engine, err := policies.NewEngine(journal, policies.Limits{
		MaxCallsPerRun:        cfg.Guardrails.MaxRequestsPerRun,
		MaxCostPerRunUSD:      cfg.Guardrails.MaxCostPerRunUSD,
		MaxInputTokensPerRun:  cfg.Guardrails.MaxInputTokensPerRun,
		MaxOutputTokensPerRun: cfg.Guardrails.MaxOutputTokensPerRun,
		MaxTotalTokensPerRun:  cfg.Guardrails.MaxTotalTokensPerRun,
		MaxDurationSeconds:    cfg.Guardrails.MaxDurationSeconds,
		MaxToolCallsPerRun:    cfg.Guardrails.MaxToolCallsPerRun,
		AllowedProviders:      cfg.Guardrails.AllowedProviders,
		AllowedModels:         cfg.Guardrails.AllowedModels,
		AllowedTools:          cfg.Guardrails.AllowedTools,
		MaxCallsPerDay:        cfg.Guardrails.MaxCallsPerDay,
		MaxCostPerDayUSD:      cfg.Guardrails.MaxCostPerDayUSD,
	})
	if err != nil {
		journal.Close()
		return nil, nil, nil, err
	}
	var recorder gateway.EventRecorder = journal
	var control *executions.HTTPHandler
	var executionAuth gateway.ExecutionAuthenticator
	var policyNotifier gateway.PolicyBlockNotifier
	if credential != nil {
		registry := executions.NewRegistry(journal)
		control = executions.NewHTTPHandler(*credential, registry, journal)
		executionAuth = registry
		policyNotifier = registry
	}
	if cfg.ControlPlane.Enabled {
		recorder = destinationRecorder{journal: journal, destination: "controlplane"}
	}
	appliedHash, hashErr := settings.HashFile(configPath)
	var settingsStore *settings.Store
	if hashErr == nil {
		settingsStore = settings.New(configPath)
	}
	handler, err := gateway.NewServer(cfg, gateway.Dependencies{
		DB: db, Getenv: os.Getenv,
		Recorder: recorder, InstallationID: journal.InstallationID(), Policy: engine,
		ConfigPath:        configPath,
		DashboardPassword: cfg.Dashboard.Password,
		Control:           control,
		ExecutionAuth:     executionAuth,
		PolicyBlocks:      journal,
		PolicyNotifier:    policyNotifier,
		Settings:          settingsStore,
		AppliedConfigHash: appliedHash,
	})
	if err != nil {
		journal.Close()
		return nil, nil, nil, err
	}
	return handler, journal, engine, nil
}

type destinationRecorder struct {
	journal     *storage.Journal
	destination string
}

func (r destinationRecorder) Record(ctx context.Context, event telemetry.Event) error {
	return r.journal.Append(ctx, event, []string{r.destination})
}
