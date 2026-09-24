package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

// SupervisionSpec binds a child process to an authenticated local core.
type SupervisionSpec struct {
	Run     RunSpec
	Control *ControlClient
}

// Supervise runs a child only while the local control stream is healthy.
func Supervise(ctx context.Context, spec SupervisionSpec) (ExecutionSummary, error) {
	return superviseWithStarter(ctx, spec, Start)
}

func superviseWithStarter(ctx context.Context, spec SupervisionSpec, start func(context.Context, RunSpec) (Process, error)) (ExecutionSummary, error) {
	if spec.Control == nil {
		return ExecutionSummary{}, errors.New("local core control client is required")
	}
	if len(spec.Run.Command) == 0 {
		return ExecutionSummary{}, errors.New("child command is required")
	}
	for key := range spec.Run.Env {
		if credentialEnvKey(key) || reservedChildEnvKey(key) {
			return ExecutionSummary{}, fmt.Errorf("child environment cannot set protected variable %q", key)
		}
	}
	registration, err := spec.Control.Register(ctx, spec.Run.RunID)
	if err != nil {
		return ExecutionSummary{}, fmt.Errorf("register execution: %w", err)
	}
	streamCtx, closeStream := context.WithCancel(ctx)
	defer closeStream()
	signals, signalFailures, err := spec.Control.Signals(streamCtx, registration)
	if err != nil {
		result := executions.FinishRequest{State: executions.StateGatewayFailed, ExitCode: -1, StopReason: "signal_unavailable", TerminationStatus: executions.TerminationNotRequired}
		_, finishErr := finishExecution(ctx, spec.Control, registration.RunID, result)
		return ExecutionSummary{}, errors.Join(fmt.Errorf("open signal stream: %w", err), finishErr)
	}
	run := spec.Run
	run.RunID = registration.RunID
	run.Env = make(map[string]string, len(spec.Run.Env)+2)
	for key, value := range spec.Run.Env {
		run.Env[key] = value
	}
	run.Env["OPENAI_API_KEY"] = registration.RunToken
	run.Env["VIRGIL_RUN_TOKEN"] = registration.RunToken
	proc, err := start(ctx, run)
	if err != nil {
		result := executions.FinishRequest{State: executions.StateFailed, ExitCode: -1, StopReason: "start_failed", TerminationStatus: executions.TerminationNotRequired}
		_, finishErr := finishExecution(ctx, spec.Control, registration.RunID, result)
		return ExecutionSummary{}, errors.Join(fmt.Errorf("start child: %w", err), finishErr)
	}
	done := make(chan ProcessResult, 1)
	go func() { done <- proc.Wait() }()
	var timer *time.Timer
	var deadline <-chan time.Time
	if run.Deadline > 0 {
		timer = time.NewTimer(run.Deadline)
		deadline = timer.C
		defer timer.Stop()
	}
	result := executions.FinishRequest{ExitCode: -1, TerminationStatus: executions.TerminationNotRequired}
	var processResult ProcessResult
	processAlreadyDone := false
	var supervisorErr error
	if err := spec.Control.MarkRunning(ctx, registration.RunID); err != nil {
		result.State = executions.StateGatewayFailed
		result.StopReason = "control_unavailable"
		supervisorErr = fmt.Errorf("mark execution running: %w", err)
	} else {
		select {
		case processResult = <-done:
			processAlreadyDone = true
			// A queued policy signal wins if the core already blocked this run.
			select {
			case signal, ok := <-signals:
				if ok && signal.Kind == executions.SignalCircuitBreak {
					result.State = executions.StateFailed
					result.StopReason = "policy_block"
				}
			default:
			}
			if result.StopReason == "" {
				result.ExitCode = processResult.ExitCode
				if processResult.ExitCode == 0 && processResult.Err == nil {
					result.State = executions.StateCompleted
				} else {
					result.State = executions.StateFailed
				}
			}
		case signal, ok := <-signals:
			if ok && signal.Kind == executions.SignalCircuitBreak {
				result.State = executions.StateFailed
				result.StopReason = "policy_block"
			} else {
				result.State = executions.StateGatewayFailed
				result.StopReason = "signal_lost"
			}
		case <-signalFailures:
			result.State = executions.StateGatewayFailed
			result.StopReason = "signal_lost"
		case <-deadline:
			result.State = executions.StateDeadline
			result.StopReason = "deadline"
		case <-ctx.Done():
			result.State = executions.StateInterrupted
			result.StopReason = "interrupted"
		}
	}
	if result.StopReason != "" {
		var termErr error
		if !processAlreadyDone {
			termCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			termErr = proc.Terminate(termCtx)
			cancel()
		}
		if termErr != nil {
			result.State = executions.StateTerminationFailed
			result.TerminationStatus = executions.TerminationFailed
			result.TerminationCode = "process_termination_failed"
			supervisorErr = errors.Join(supervisorErr, fmt.Errorf("terminate child process %d: %w", proc.PID(), termErr))
		} else {
			result.TerminationStatus = executions.TerminationSucceeded
			if !processAlreadyDone {
				processResult = <-done
			}
			result.ExitCode = processResult.ExitCode
		}
	}
	summary, finishErr := finishExecution(ctx, spec.Control, registration.RunID, result)
	if finishErr != nil {
		supervisorErr = errors.Join(supervisorErr, fmt.Errorf("finish execution: %w", finishErr))
	}
	return summary, supervisorErr
}

func finishExecution(ctx context.Context, client *ControlClient, runID string, result executions.FinishRequest) (ExecutionSummary, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return client.Finish(finishCtx, runID, result)
}

func credentialEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY") || strings.Contains(upper, "_KEY_") || upper == "KEY"
}

func reservedChildEnvKey(key string) bool {
	switch strings.ToUpper(key) {
	case "VIRGIL_RUN_ID", "VIRGIL_GATEWAY_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE", "ANTHROPIC_BASE_URL":
		return true
	default:
		return false
	}
}
