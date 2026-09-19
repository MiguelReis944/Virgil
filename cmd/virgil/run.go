package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/runner"
)

// runRun handles the "virgil run" subcommand, which starts a child process
// under gateway supervision. The gateway must already be running.
func runRun(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	gatewayURL := flags.String("gateway", "http://127.0.0.1:8787", "gateway URL to pass to the child as VIRGIL_GATEWAY_URL")
	runID := flags.String("run-id", "", "run correlation ID (generated if empty)")
	deadline := flags.Duration("deadline", 0, "wall-clock limit for the child process (e.g. 30m); 0 means none")
	envFlag := flags.String("env", "", "extra KEY=VALUE pairs for the child, comma-separated")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cmd := flags.Args()
	if len(cmd) == 0 {
		return fmt.Errorf("usage: virgil run [flags] -- <command> [args...]")
	}

	extraEnv := make(map[string]string)
	if *envFlag != "" {
		for _, pair := range strings.Split(*envFlag, ",") {
			pair = strings.TrimSpace(pair)
			k, v, ok := strings.Cut(pair, "=")
			if !ok || k == "" {
				return fmt.Errorf("invalid --env entry %q: expected KEY=VALUE", pair)
			}
			extraEnv[k] = v
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	policyChan := make(chan struct{})
	spec := runner.RunSpec{
		Command:    cmd,
		Env:        extraEnv,
		RunID:      *runID,
		GatewayURL: *gatewayURL,
		Deadline:   *deadline,
		PolicyStop: policyChan,
	}

	slog.Info("starting supervised run", "command", cmd[0], "gateway", *gatewayURL)
	start := time.Now()
	result, err := runner.Run(ctx, spec)
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("run %s: %w", result.RunID, err)
	}
	switch result.Stopped {
	case runner.StopDeadline:
		slog.Warn("child stopped: deadline exceeded", "run_id", result.RunID, "elapsed", elapsed)
	case runner.StopPolicyBlock:
		slog.Warn("child stopped: policy block", "run_id", result.RunID)
	case runner.StopContext:
		slog.Info("child stopped: interrupted", "run_id", result.RunID)
	default:
		slog.Info("child exited", "run_id", result.RunID, "exit_code", result.ExitCode, "elapsed", elapsed)
	}
	if result.ExitCode != 0 {
		os.Exit(result.ExitCode)
	}
	return nil
}
