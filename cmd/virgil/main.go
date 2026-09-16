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
	"github.com/MiguelReis944/Virgil/internal/gateway"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return fmt.Errorf("usage: virgil serve --config <path>")
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
	slog.Info("gateway listening", "address", cfg.Server.Listen)
	return gateway.ListenAndServe(ctx, cfg.Server.Listen, handler)
}

func buildHandler(cfg config.Config, db *sql.DB) (http.Handler, *storage.Journal, error) {
	journal, err := storage.NewJournal(db)
	if err != nil {
		return nil, nil, err
	}
	handler, err := gateway.NewServer(cfg, gateway.Dependencies{
		DB: db, Getenv: os.Getenv,
		Recorder: journal, InstallationID: journal.InstallationID(),
	})
	if err != nil {
		journal.Close()
		return nil, nil, err
	}
	return handler, journal, nil
}
