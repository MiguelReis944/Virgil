package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

func runTail(args []string) error {
	flags := flag.NewFlagSet("tail", flag.ContinueOnError)
	n := flags.Int64("n", 20, "number of events to show")
	format := flags.String("format", "text", "output format: text or json")
	configPath := flags.String("config", "virgil.toml", "path to TOML configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("--format must be text or json")
	}
	if *n < 1 {
		*n = 1
	}
	if *n > 1000 {
		*n = 1000
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
	events, err := journal.ListRecent(context.Background(), *n)
	if err != nil {
		return err
	}
	for _, ev := range events {
		switch *format {
		case "json":
			if err := json.NewEncoder(os.Stdout).Encode(ev); err != nil {
				return err
			}
		default:
			latency := ""
			if ev.LatencyMS > 0 {
				latency = fmt.Sprintf("%dms", ev.LatencyMS)
			}
			cost := ""
			if ev.ActualCost != nil && *ev.ActualCost != "" {
				cost = *ev.ActualCost
			} else if ev.EstimatedCost != nil && *ev.EstimatedCost != "" {
				cost = "~" + *ev.EstimatedCost
			}
			fmt.Printf("%s %s/%s %s %s %s\n",
				ev.CreatedAt.Format("2006-01-02T15:04:05Z"),
				ev.Provider, ev.RequestedModel,
				ev.Status, latency, cost,
			)
		}
	}
	return nil
}
