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

	"github.com/MiguelReis944/Virgil/internal/app"
	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/export"
	"github.com/MiguelReis944/Virgil/internal/policies"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("Virgil stopped", "error", err)
		os.Exit(1)
	}
}

type commandKind string

const (
	commandCore   commandKind = "core"
	commandRun    commandKind = "run"
	commandEvents commandKind = "events"
	commandInit   commandKind = "init"
	commandTail   commandKind = "tail"
)

func classifyCommand(args []string) (commandKind, []string, error) {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return commandCore, args, nil
	}
	switch args[0] {
	case "serve":
		return commandCore, args[1:], nil
	case "run":
		return commandRun, args[1:], nil
	case "events":
		return commandEvents, args[1:], nil
	case "init":
		return commandInit, args[1:], nil
	case "tail":
		return commandTail, args[1:], nil
	default:
		return "", nil, fmt.Errorf("unknown command: %s (available: serve, run, events, init, tail)", args[0])
	}
}

func run(args []string) error {
	kind, rest, err := classifyCommand(args)
	if err != nil {
		return err
	}
	switch kind {
	case commandRun:
		return runRun(rest)
	case commandEvents:
		return runEvents(rest)
	case commandInit:
		return runInit(rest)
	case commandTail:
		return runTail(rest)
	}
	flags := flag.NewFlagSet("virgil", flag.ContinueOnError)
	configPath := flags.String("config", "virgil.toml", "path to local TOML configuration")
	envPath := flags.String("env-file", ".env", "path to .env file (ignored if missing)")
	flags.StringVar(envPath, "env", ".env", "alias for --env-file")
	noOpen := flags.Bool("no-open", false, "start the local panel without opening a browser")
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q: use virgil run -- <command> for an agent", flags.Arg(0))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return app.RunCore(ctx, app.CoreOptions{
		ConfigPath:        *configPath,
		EnvPath:           *envPath,
		OpenPanel:         !*noOpen,
		StartControlPlane: startControlPlane,
	})
}

func startControlPlane(ctx context.Context, cfg config.Config, journal *storage.Journal, engine *policies.Engine) (func(), error) {
	if err := loadCachedControlPlanePolicy(ctx, journal, engine); err != nil {
		return nil, fmt.Errorf("cached control plane policy: %w", err)
	}
	client, err := configuredControlPlaneClient(cfg.ControlPlane)
	if err != nil {
		return nil, fmt.Errorf("control plane credential: %w", err)
	}
	deliveryCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runControlPlaneDelivery(deliveryCtx, journal, engine, client, cfg.ControlPlane.AllowedFields)
	}()
	return func() { cancel(); <-done }, nil
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
	return app.BuildHandlerWithEngine(cfg, configPath, db, readDB)
}
