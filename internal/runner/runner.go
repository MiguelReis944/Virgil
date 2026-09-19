// Package runner starts and supervises child processes under a Virgil gateway.
package runner

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// StopReason describes why a supervised process was stopped before it exited.
type StopReason string

const (
	StopNone        StopReason = ""
	StopDeadline    StopReason = "deadline"
	StopPolicyBlock StopReason = "policy_block"
	StopContext     StopReason = "context_cancelled"
)

// RunSpec configures a supervised child process.
type RunSpec struct {
	// Command is the executable and its arguments.
	Command []string

	// Env contains extra environment variables merged onto the inherited
	// environment (after credential vars are stripped). Provider API keys that
	// the child is allowed to use must be passed here explicitly.
	Env map[string]string

	// RunID is the correlation identifier injected as VIRGIL_RUN_ID.
	// A new random ID is generated when empty.
	RunID string

	// GatewayURL is injected as VIRGIL_GATEWAY_URL. Empty is allowed when
	// running outside a gateway context.
	GatewayURL string

	// Deadline is the maximum wall-clock duration. Zero means no limit.
	Deadline time.Duration

	// PolicyStop is closed by the caller when a local policy block fires for
	// this run. The runner terminates the child immediately on close.
	PolicyStop <-chan struct{}
}

// RunResult summarises a completed supervised run.
type RunResult struct {
	RunID    string
	ExitCode int
	Stopped  StopReason
}

// Run starts the child process described by spec, supervises it, and returns
// after the child exits or is stopped.
//
// Stdout and stderr of the child are discarded; the runner never persists or
// logs their content. The child inherits stdin from the parent process.
//
// The caller must set spec.PolicyStop to receive policy-block signals; a nil
// channel means policy blocks are not monitored.
func Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	runID := spec.RunID
	if runID == "" {
		id, err := telemetry.NewID(16)
		if err != nil {
			return RunResult{}, err
		}
		runID = "run_" + id
	}

	if len(spec.Command) == 0 {
		return RunResult{RunID: runID}, nil
	}

	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)

	// Build environment: inherit parent (minus credential vars), then apply explicit overrides.
	raw := os.Environ()
	env := make([]string, 0, len(raw))
	for _, kv := range raw {
		upper := strings.ToUpper(kv)
		if strings.Contains(upper, "API_KEY=") ||
			strings.Contains(upper, "API_TOKEN=") ||
			strings.Contains(upper, "SECRET=") ||
			strings.HasPrefix(upper, "VIRGIL_LOCAL_APP_TOKEN=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "VIRGIL_RUN_ID="+runID)
	if spec.GatewayURL != "" {
		env = append(env, "VIRGIL_GATEWAY_URL="+spec.GatewayURL)
		env = append(env, "OPENAI_BASE_URL="+spec.GatewayURL+"/v1")
		env = append(env, "OPENAI_API_BASE="+spec.GatewayURL+"/v1")
		env = append(env, "ANTHROPIC_BASE_URL="+spec.GatewayURL)
	}
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Stdin is inherited; stdout and stderr mirror to the parent process.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return RunResult{RunID: runID}, err
	}

	// Wait for the child in a goroutine so we can race against other signals.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timer <-chan time.Time
	if spec.Deadline > 0 {
		t := time.NewTimer(spec.Deadline)
		defer t.Stop()
		timer = t.C
	}

	var stopped StopReason
	select {
	case <-done:
		// Child exited on its own.
	case <-timer:
		stopped = StopDeadline
		kill(cmd, done)
	case <-spec.PolicyStop:
		stopped = StopPolicyBlock
		kill(cmd, done)
	case <-ctx.Done():
		stopped = StopContext
		kill(cmd, done)
	}

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return RunResult{RunID: runID, ExitCode: exitCode, Stopped: stopped}, nil
}

// kill terminates the child process group gracefully then forcefully.
// done is the same channel the Run loop uses to receive cmd.Wait(); kill blocks
// until the process exits so the caller does not need a separate <-done.
// On Unix, SIGTERM is sent first and the process is given 3 seconds to exit
// before a SIGKILL is delivered. On Windows, Kill() is used directly since
// SIGTERM does not exist.
func kill(cmd *exec.Cmd, done <-chan error) {
	if cmd.Process == nil {
		<-done
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
		<-done
		return
	}
	// Unix: try graceful shutdown first.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
		// Exited cleanly after SIGTERM.
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
