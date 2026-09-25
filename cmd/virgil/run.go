package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/executions"
	"github.com/MiguelReis944/Virgil/internal/runner"
)

// runRun handles the "virgil run" subcommand, which starts a child process
// under gateway supervision. The gateway must already be running.
func runRun(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	defaultPath, err := defaultConfigPath()
	if err != nil {
		return err
	}
	configPath := flags.String("config", defaultPath, "path to local TOML configuration")
	address := flags.String("address", "", "loopback core address (defaults to server.listen)")
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
	if *deadline < 0 {
		return fmt.Errorf("--deadline cannot be negative")
	}

	extraEnv := make(map[string]string)
	if *envFlag != "" {
		for _, pair := range strings.Split(*envFlag, ",") {
			pair = strings.TrimSpace(pair)
			k, v, ok := strings.Cut(pair, "=")
			if !ok || k == "" {
				return fmt.Errorf("invalid --env entry: expected KEY=VALUE")
			}
			extraEnv[k] = v
		}
	}
	cfg, err := config.Load(*configPath, os.Getenv)
	if err != nil {
		return err
	}
	coreAddress := *address
	if coreAddress == "" {
		coreAddress = cfg.Server.Listen
	}
	if !strings.Contains(coreAddress, "://") {
		coreAddress = "http://" + coreAddress
	}
	credentialBytes, err := os.ReadFile(filepath.Join(filepath.Dir(cfg.Storage.Path), "control.token"))
	if err != nil {
		return fmt.Errorf("read local control credential: %w", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(credentialBytes))
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid local control credential")
	}
	client, err := runner.NewControlClient(coreAddress, string(credentialBytes))
	if err != nil {
		return err
	}
	coreAddress = client.BaseURL()
	var providerCredentialEnv []string
	for _, provider := range cfg.Providers {
		if provider.APIKeyEnv != "" {
			providerCredentialEnv = append(providerCredentialEnv, provider.APIKeyEnv)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	spec := runner.SupervisionSpec{Run: runner.RunSpec{
		Command: cmd, Env: extraEnv, ProviderCredentialEnv: providerCredentialEnv, RunID: *runID, GatewayURL: coreAddress, Deadline: *deadline,
	}, Control: client}
	slog.Info("starting supervised run", "command", cmd[0], "gateway", coreAddress)
	start := time.Now()
	result, err := runner.Supervise(ctx, spec)
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("supervised run: %w", err)
	}
	if result.ExitCode == nil {
		return fmt.Errorf("local core returned no child exit code")
	}
	slog.Info("execution finished", "run_id", result.RunID, "state", result.State, "exit_code", *result.ExitCode, "elapsed", elapsed)
	if code := runExitCode(result); code != 0 {
		os.Exit(code)
	}
	return nil
}

func runExitCode(summary runner.ExecutionSummary) int {
	if summary.ExitCode == nil || *summary.ExitCode < 0 {
		return 1
	}
	if *summary.ExitCode != 0 {
		return *summary.ExitCode
	}
	if summary.State != executions.StateCompleted {
		return 1
	}
	return *summary.ExitCode
}
