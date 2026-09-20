package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/export"
	"github.com/MiguelReis944/Virgil/internal/gateway"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: virgil <serve|run|events> ...")
	}
	switch args[0] {
	case "events":
		return runEvents(args[1:])
	case "run":
		return runRun(args[1:])
	case "init":
		return runInit(args[1:])
	case "tail":
		return runTail(args[1:])
	case "serve":
	default:
		return fmt.Errorf("unknown command: %s (available: serve, run, events, init, tail)", args[0])
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "virgil.toml", "path to TOML configuration (default: virgil.toml in current directory)")
	envFile := flags.String("env", ".env", "path to .env file (loaded before config; ignored if missing)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	loadDotEnv(*envFile)
	cfg, err := config.Load(*configPath, os.Getenv)
	if err != nil {
		slog.Warn("config invalid or missing; starting setup server", "reason", err, "url", "http://127.0.0.1:8787/setup")
		return runSetupServer(*configPath)
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
	handler, journal, engine, err := buildHandlerWithEngine(cfg, *configPath, db, readDB)
	if err != nil {
		return err
	}
	defer journal.Close()
	if cfg.Storage.RetentionDays > 0 {
		if _, err := journal.Prune(context.Background(), time.Now().AddDate(0, 0, -cfg.Storage.RetentionDays)); err != nil {
			slog.Warn("retention prune failed; continuing", "error", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if cfg.ControlPlane.Enabled {
		if err := loadCachedControlPlanePolicy(ctx, journal, engine); err != nil {
			return fmt.Errorf("cached control plane policy: %w", err)
		}
		client, err := configuredControlPlaneClient(cfg.ControlPlane)
		if err != nil {
			return fmt.Errorf("control plane credential: %w", err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			runControlPlaneDelivery(ctx, journal, engine, client, cfg.ControlPlane.AllowedFields)
		}()
		defer func() { stop(); <-done }()
	}
	slog.Info("gateway listening", "address", cfg.Server.Listen)
	return gateway.ListenAndServe(ctx, cfg.Server.Listen, handler)
}

// loadDotEnv reads KEY=VALUE pairs from path and sets them via os.Setenv.
// Lines starting with # and empty lines are ignored. Already-set variables
// are not overwritten (same behaviour as dotenv tools).
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // file missing is fine
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
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	slog.Info("loaded .env", "path", path)
}

func runSetupServer(configPath string) error {
	mux := http.NewServeMux()
	setupH := gateway.NewSetupHandler(configPath)
	mux.HandleFunc("GET /setup", setupH)
	mux.HandleFunc("POST /setup", setupH)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/setup", http.StatusFound)
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	const addr = "127.0.0.1:8787"
	slog.Info("setup server listening", "address", addr)
	return gateway.ListenAndServe(ctx, addr, mux)
}

func runEvents(args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return fmt.Errorf("usage: virgil events export --config <path> --format jsonl --output <path> --fields <f1,f2,...>")
	}
	flags := flag.NewFlagSet("events export", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to local TOML configuration")
	format := flags.String("format", "", "export format (jsonl)")
	output := flags.String("output", "", "output file path")
	fields := flags.String("fields", "", "comma-separated list of allowed fields")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *format != "jsonl" {
		return fmt.Errorf("--format must be jsonl")
	}
	if *output == "" {
		return fmt.Errorf("--output is required")
	}
	var allowed []string
	if *fields != "" {
		for _, f := range splitFields(*fields) {
			if f != "" {
				allowed = append(allowed, f)
			}
		}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("--fields is required")
	}
	cfg, err := config.Load(*configPath, os.Getenv)
	if err != nil {
		return err
	}
	db, err := storage.Open(cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	journal, err := storage.NewJournal(db)
	if err != nil {
		return err
	}
	defer journal.Close()
	return export.JSONL(context.Background(), journal, *output, allowed)
}

func splitFields(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func buildHandler(cfg config.Config, db *sql.DB) (http.Handler, *storage.Journal, error) {
	handler, journal, _, err := buildHandlerWithEngine(cfg, "", db, nil)
	return handler, journal, err
}

func buildHandlerWithEngine(cfg config.Config, configPath string, db, readDB *sql.DB) (http.Handler, *storage.Journal, *policies.Engine, error) {
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
	if cfg.ControlPlane.Enabled {
		recorder = destinationRecorder{journal: journal, destination: "controlplane"}
	}
	handler, err := gateway.NewServer(cfg, gateway.Dependencies{
		DB: db, Getenv: os.Getenv,
		Recorder: recorder, InstallationID: journal.InstallationID(), Policy: engine,
		ConfigPath:        configPath,
		DashboardPassword: cfg.Dashboard.Password,
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
