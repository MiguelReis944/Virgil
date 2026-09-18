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
	case "serve":
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to local TOML configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	cfg, err := config.Load(*configPath, os.Getenv)
	if err != nil {
		return err
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
	handler, journal, err := buildHandler(cfg, db)
	if err != nil {
		return err
	}
	defer journal.Close()
	if cfg.Storage.RetentionDays > 0 {
		if _, err := journal.Prune(context.Background(), time.Now().AddDate(0, 0, -cfg.Storage.RetentionDays)); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if cfg.ControlPlane.Enabled {
		client, err := configuredControlPlaneClient(cfg.ControlPlane)
		if err != nil {
			return fmt.Errorf("control plane credential: %w", err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			runControlPlaneDelivery(ctx, journal, client, cfg.ControlPlane.AllowedFields)
		}()
		defer func() { stop(); <-done }()
	}
	slog.Info("gateway listening", "address", cfg.Server.Listen)
	return gateway.ListenAndServe(ctx, cfg.Server.Listen, handler)
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
	journal, err := storage.NewJournal(db)
	if err != nil {
		return nil, nil, err
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
	})
	if err != nil {
		journal.Close()
		return nil, nil, err
	}
	var recorder gateway.EventRecorder = journal
	if cfg.ControlPlane.Enabled {
		recorder = destinationRecorder{journal: journal, destination: "controlplane"}
	}
	handler, err := gateway.NewServer(cfg, gateway.Dependencies{
		DB: db, Getenv: os.Getenv,
		Recorder: recorder, InstallationID: journal.InstallationID(), Policy: engine,
	})
	if err != nil {
		journal.Close()
		return nil, nil, err
	}
	return handler, journal, nil
}

type destinationRecorder struct {
	journal     *storage.Journal
	destination string
}

func (r destinationRecorder) Record(ctx context.Context, event telemetry.Event) error {
	return r.journal.Append(ctx, event, []string{r.destination})
}
