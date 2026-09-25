package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

func executionJournal(t *testing.T) *Journal {
	t.Helper()
	db, j := openJournal(t, filepath.Join(t.TempDir(), "virgil.db"))
	t.Cleanup(func() { j.Close(); db.Close() })
	return j
}

func TestExecutionTransitionsAndDetail(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	started := time.Date(2026, 9, 23, 12, 0, 0, 123, time.FixedZone("offset", -3*3600))
	ended := started.Add(time.Second)
	if err := j.StartExecution(ctx, "run_a", started); err != nil {
		t.Fatal(err)
	}
	if err := j.MarkExecutionRunning(ctx, "run_a"); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateCompleted, EndedAt: ended, ExitCode: 0, TerminationStatus: executions.TerminationNotRequired}); err != nil {
		t.Fatal(err)
	}
	got, err := j.Execution(ctx, "run_a")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != executions.StateCompleted || got.EndedAt == nil || !got.EndedAt.Equal(ended) || got.ExitCode == nil || *got.ExitCode != 0 || !got.StartedAt.Equal(started) || got.StartedAt.Location() != time.UTC {
		t.Fatalf("execution=%+v", got)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateFailed, EndedAt: ended}); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("duplicate finish: %v", err)
	}
	if err := j.MarkExecutionRunning(ctx, "run_a"); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("terminal running: %v", err)
	}
}

func TestExecutionStartFailureAndDuplicateID(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := j.StartExecution(ctx, "run_a", now); err != nil {
		t.Fatal(err)
	}
	if err := j.StartExecution(ctx, "run_a", now); !errors.Is(err, executions.ErrRunExists) {
		t.Fatalf("duplicate ID error = %v, want ErrRunExists", err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateFailed, EndedAt: now.Add(time.Second), ExitCode: 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "missing", State: executions.StateFailed, EndedAt: now}); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("missing finish: %v", err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateBlocked, EndedAt: now, TerminationStatus: executions.TerminationSucceeded}); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("terminal overwrite: %v", err)
	}
}

func TestExecutionPolicyBlockFollowUp(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status executions.TerminationStatus
		final  executions.State
	}{
		{"succeeded", executions.TerminationSucceeded, executions.StateBlocked},
		{"failed", executions.TerminationFailed, executions.StateTerminationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := executionJournal(t)
			ctx := context.Background()
			now := time.Now().UTC()
			if err := j.StartExecution(ctx, "run_a", now); err != nil {
				t.Fatal(err)
			}
			if err := j.MarkExecutionRunning(ctx, "run_a"); err != nil {
				t.Fatal(err)
			}
			notice := executions.PolicyBlockNotice{RunID: "run_a", Policy: "max_requests", Reason: "threshold", Attempt: 3, Threshold: 2, BlockedCallEstimateUSD: "0.02", OccurredAt: now.Add(time.Second)}
			if err := j.BlockExecution(ctx, notice); err != nil {
				t.Fatal(err)
			}
			got, err := j.Execution(ctx, "run_a")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != executions.StateBlocked || got.Policy != notice.Policy || got.PolicyReason != notice.Reason || got.PolicyAttempt == nil || *got.PolicyAttempt != 3 || got.PolicyThreshold == nil || *got.PolicyThreshold != 2 || got.EndedAt != nil {
				t.Fatalf("blocked=%+v", got)
			}
			if err := j.BlockExecution(ctx, notice); !errors.Is(err, executions.ErrInvalidTransition) {
				t.Fatalf("duplicate block: %v", err)
			}
			result := executions.ExecutionResult{RunID: "run_a", State: tc.final, EndedAt: now.Add(2 * time.Second), ExitCode: 1, StopReason: "policy_block", TerminationStatus: tc.status}
			if tc.status == executions.TerminationFailed {
				result.TerminationErrorCode = "process_tree"
			}
			if err := j.FinishExecution(ctx, result); err != nil {
				t.Fatal(err)
			}
			got, err = j.Execution(ctx, "run_a")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.final || got.TerminationStatus != tc.status || got.Policy != notice.Policy || got.BlockedCallEstimateUSD != notice.BlockedCallEstimateUSD || got.EndedAt == nil {
				t.Fatalf("followup=%+v", got)
			}
			if err := j.FinishExecution(ctx, result); !errors.Is(err, executions.ErrInvalidTransition) {
				t.Fatalf("duplicate followup: %v", err)
			}
		})
	}
}

func TestExecutionRunningTerminationFailure(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := j.StartExecution(ctx, "run_a", now); err != nil {
		t.Fatal(err)
	}
	if err := j.MarkExecutionRunning(ctx, "run_a"); err != nil {
		t.Fatal(err)
	}
	result := executions.ExecutionResult{RunID: "run_a", State: executions.StateTerminationFailed, EndedAt: now.Add(time.Second), ExitCode: 1, StopReason: "deadline", TerminationStatus: executions.TerminationFailed, TerminationErrorCode: "process_tree"}
	if err := j.FinishExecution(ctx, result); err != nil {
		t.Fatal(err)
	}
	got, err := j.Execution(ctx, "run_a")
	if err != nil || got.State != executions.StateTerminationFailed || got.TerminationErrorCode != "process_tree" || got.Policy != "" {
		t.Fatalf("execution=%+v err=%v", got, err)
	}
	if err := j.FinishExecution(ctx, result); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("duplicate failure: %v", err)
	}
}

func TestExecutionStartingTerminationFailure(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := j.StartExecution(ctx, "run_starting", now); err != nil {
		t.Fatal(err)
	}
	result := executions.ExecutionResult{RunID: "run_starting", State: executions.StateTerminationFailed, EndedAt: now.Add(time.Second), ExitCode: -1, StopReason: "signal_lost", TerminationStatus: executions.TerminationFailed, TerminationErrorCode: "process_termination_failed"}
	if err := j.FinishExecution(ctx, result); err != nil {
		t.Fatal(err)
	}
	got, err := j.Execution(ctx, "run_starting")
	if err != nil || got.State != executions.StateTerminationFailed || got.StopReason != "signal_lost" || got.TerminationStatus != executions.TerminationFailed || got.TerminationErrorCode != "process_termination_failed" {
		t.Fatalf("execution=%+v err=%v", got, err)
	}
	if err := j.FinishExecution(ctx, result); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("duplicate failure: %v", err)
	}
	if err := j.MarkExecutionRunning(ctx, "run_starting"); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("running after failure: %v", err)
	}
}

func TestExecutionStartingRejectsUnnormalizedTerminationCode(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := j.StartExecution(ctx, "run_bad_code", now); err != nil {
		t.Fatal(err)
	}
	result := executions.ExecutionResult{RunID: "run_bad_code", State: executions.StateTerminationFailed, EndedAt: now.Add(time.Second), StopReason: "control_unavailable", TerminationStatus: executions.TerminationFailed, TerminationErrorCode: "kill failed: secret detail"}
	if err := j.FinishExecution(ctx, result); err == nil {
		t.Fatal("unnormalized termination error code accepted")
	}
	got, err := j.Execution(ctx, "run_bad_code")
	if err != nil || got.State != executions.StateStarting || got.EndedAt != nil {
		t.Fatalf("execution=%+v err=%v", got, err)
	}
}

func TestExecutionInvalidTransitionsAndInputs(t *testing.T) {
	j := executionJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := j.StartExecution(ctx, "run_a", now); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateCompleted, EndedAt: now}); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("starting completed: %v", err)
	}
	if err := j.BlockExecution(ctx, executions.PolicyBlockNotice{RunID: "run_a", Policy: "max_requests", Reason: "threshold", Attempt: 1, Threshold: 1, OccurredAt: now}); !errors.Is(err, executions.ErrInvalidTransition) {
		t.Fatalf("starting blocked: %v", err)
	}
	if err := j.MarkExecutionRunning(ctx, "run_a"); err != nil {
		t.Fatal(err)
	}
	if err := j.BlockExecution(ctx, executions.PolicyBlockNotice{RunID: "run_a", Policy: "max_requests", Reason: "threshold", Attempt: -1, Threshold: 1, OccurredAt: now}); err == nil {
		t.Fatal("negative attempt accepted")
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: executions.StateCompleted, EndedAt: now, TerminationStatus: executions.TerminationFailed, TerminationErrorCode: "process_tree"}); err == nil {
		t.Fatal("ordinary completion accepted failed termination")
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "run_a", State: "invalid", EndedAt: now}); err == nil {
		t.Fatal("unknown state accepted")
	}
	if err := j.StartExecution(ctx, "", now); err == nil {
		t.Fatal("empty run ID accepted")
	}
}

func TestListExecutionsAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, j := openJournal(t, path)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, id := range []string{"first", "second", "third"} {
		if err := j.StartExecution(ctx, id, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.MarkExecutionRunning(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if err := j.FinishExecution(ctx, executions.ExecutionResult{RunID: "third", State: executions.StateFailed, EndedAt: now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	rows, err := j.ListExecutions(ctx, 2)
	if err != nil || len(rows) != 2 || rows[0].RunID != "third" || rows[1].RunID != "second" {
		t.Fatalf("list=%+v err=%v", rows, err)
	}
	j.Close()
	db.Close()
	db, j = openJournal(t, path)
	defer j.Close()
	defer db.Close()
	if err := j.RecoverExecutions(ctx, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		got, err := j.Execution(ctx, id)
		if err != nil || got.State != executions.StateInterrupted || got.StopReason != "virgil_restart" || got.EndedAt == nil {
			t.Fatalf("recovered %s=%+v err=%v", id, got, err)
		}
	}
	got, err := j.Execution(ctx, "third")
	if err != nil || got.State != executions.StateFailed {
		t.Fatalf("terminal=%+v err=%v", got, err)
	}
	if err := j.RecoverExecutions(ctx, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = j.Execution(ctx, "first")
	if err != nil || !got.EndedAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("recovery idempotence=%+v err=%v", got, err)
	}
}

func TestListExecutionsFilteredByStateAndStartTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, journal := openJournal(t, path)
	defer journal.Close()
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, fixture := range []struct {
		id      string
		started time.Time
		state   executions.State
	}{
		{id: "old_running", started: now.Add(-3 * time.Hour), state: executions.StateRunning},
		{id: "recent_running", started: now.Add(-30 * time.Minute), state: executions.StateRunning},
		{id: "recent_completed", started: now.Add(-15 * time.Minute), state: executions.StateCompleted},
	} {
		if err := journal.StartExecution(ctx, fixture.id, fixture.started); err != nil {
			t.Fatal(err)
		}
		if err := journal.MarkExecutionRunning(ctx, fixture.id); err != nil {
			t.Fatal(err)
		}
		if fixture.state == executions.StateCompleted {
			if err := journal.FinishExecution(ctx, executions.ExecutionResult{
				RunID: fixture.id, State: fixture.state, EndedAt: fixture.started.Add(time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows, err := journal.ListExecutionsFiltered(ctx, ExecutionListFilter{
		State: executions.StateRunning,
		Since: now.Add(-time.Hour),
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RunID != "recent_running" {
		t.Fatalf("rows=%+v", rows)
	}

	if _, err := journal.ListExecutionsFiltered(ctx, ExecutionListFilter{State: "unknown", Limit: 10}); err == nil {
		t.Fatal("invalid state accepted")
	}
	if _, err := journal.ListExecutionsFiltered(ctx, ExecutionListFilter{Limit: 0}); err == nil {
		t.Fatal("invalid limit accepted")
	}
}
