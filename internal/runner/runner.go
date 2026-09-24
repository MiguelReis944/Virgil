// Package runner starts and supervises child processes under a Virgil gateway.
package runner

import (
	"context"
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

	// ProviderCredentialEnv names configured provider credentials to remove
	// from the inherited environment. Explicit Env entries are still passed.
	ProviderCredentialEnv []string

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
// Stdout and stderr of the child are forwarded to the parent's terminal; the
// runner never persists or logs their content. The child inherits stdin.
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

	spec.RunID = runID
	proc, err := Start(ctx, spec)
	if err != nil {
		return RunResult{RunID: runID}, err
	}
	return supervise(ctx, spec, runID, proc)
}

func supervise(ctx context.Context, spec RunSpec, runID string, proc Process) (RunResult, error) {
	done := make(chan ProcessResult, 1)
	go func() { done <- proc.Wait() }()

	var timer <-chan time.Time
	if spec.Deadline > 0 {
		t := time.NewTimer(spec.Deadline)
		defer t.Stop()
		timer = t.C
	}

	var stopped StopReason
	var result ProcessResult
	select {
	case result = <-done:
		// Child exited on its own.
	case <-timer:
		stopped = StopDeadline
	case <-spec.PolicyStop:
		stopped = StopPolicyBlock
	case <-ctx.Done():
		stopped = StopContext
	}
	if stopped != StopNone {
		if err := proc.Terminate(context.Background()); err != nil {
			return RunResult{RunID: runID, ExitCode: -1, Stopped: stopped}, err
		}
		result = proc.Wait()
	}
	return RunResult{RunID: runID, ExitCode: result.ExitCode, Stopped: stopped}, nil
}
