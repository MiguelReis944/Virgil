package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/MiguelReis944/Virgil/internal/executions"
)

var _ executions.Store = (*Journal)(nil)

// StartExecution creates a durable starting row for a supervised run.
func (j *Journal) StartExecution(ctx context.Context, runID string, startedAt time.Time) error {
	if runID == "" || startedAt.IsZero() {
		return fmt.Errorf("start execution: invalid run ID or time")
	}
	_, err := j.db.ExecContext(ctx,
		`INSERT INTO executions (run_id, state, started_at_unix_ns) VALUES (?, 'starting', ?)`,
		runID, startedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("start execution: %w", err)
	}
	return nil
}

// MarkExecutionRunning moves a starting run to running exactly once.
func (j *Journal) MarkExecutionRunning(ctx context.Context, runID string) error {
	result, err := j.db.ExecContext(ctx,
		`UPDATE executions SET state='running' WHERE run_id=? AND state='starting'`, runID)
	return executionUpdateResult("mark execution running", result, err)
}

// BlockExecution stores the first policy decision and its details atomically.
func (j *Journal) BlockExecution(ctx context.Context, notice executions.PolicyBlockNotice) error {
	if notice.RunID == "" || notice.Policy == "" || notice.Reason == "" || notice.Attempt < 0 || notice.Threshold < 0 || notice.OccurredAt.IsZero() {
		return fmt.Errorf("block execution: invalid policy notice")
	}
	result, err := j.db.ExecContext(ctx, `UPDATE executions SET
		state='blocked', policy=?, policy_reason=?, policy_attempt=?, policy_threshold=?,
		blocked_call_estimate_usd=NULLIF(?, '')
		WHERE run_id=? AND state='running'`,
		notice.Policy, notice.Reason, notice.Attempt, notice.Threshold,
		notice.BlockedCallEstimateUSD, notice.RunID)
	return executionUpdateResult("block execution", result, err)
}

// FinishExecution records a terminal result. A blocked row accepts one narrow
// follow-up carrying the process tree termination outcome.
func (j *Journal) FinishExecution(ctx context.Context, result executions.ExecutionResult) error {
	if result.RunID == "" || result.EndedAt.IsZero() || !validTerminationStatus(result.TerminationStatus) {
		return fmt.Errorf("finish execution: invalid result")
	}
	endedAt := result.EndedAt.UTC().UnixNano()
	var updated sql.Result
	var err error
	switch result.State {
	case executions.StateBlocked:
		if result.TerminationStatus != executions.TerminationSucceeded || result.TerminationErrorCode != "" {
			return fmt.Errorf("finish execution: invalid blocked termination")
		}
		updated, err = j.db.ExecContext(ctx, `UPDATE executions SET
			ended_at_unix_ns=?, exit_code=?, stop_reason=NULLIF(?, ''),
			termination_status='succeeded'
			WHERE run_id=? AND state='blocked' AND termination_status IS NULL`,
			endedAt, result.ExitCode, result.StopReason, result.RunID)
	case executions.StateTerminationFailed:
		if result.TerminationStatus != executions.TerminationFailed || !normalizedTerminationCode(result.TerminationErrorCode) {
			return fmt.Errorf("finish execution: invalid failed termination")
		}
		updated, err = j.db.ExecContext(ctx, `UPDATE executions SET
			state='termination_failed', ended_at_unix_ns=?, exit_code=?, stop_reason=NULLIF(?, ''),
			termination_status='failed', termination_error_code=?
			WHERE run_id=? AND state IN ('starting', 'blocked', 'running') AND termination_status IS NULL`,
			endedAt, result.ExitCode, result.StopReason, result.TerminationErrorCode, result.RunID)
	case executions.StateCompleted, executions.StateFailed, executions.StateDeadline,
		executions.StateInterrupted, executions.StateGatewayFailed:
		if result.TerminationStatus == executions.TerminationFailed || result.TerminationErrorCode != "" {
			return fmt.Errorf("finish execution: failed termination requires termination_failed state")
		}
		if result.State == executions.StateFailed {
			updated, err = j.db.ExecContext(ctx, `UPDATE executions SET
				state=?, ended_at_unix_ns=?, exit_code=?, stop_reason=NULLIF(?, ''),
				termination_status=NULLIF(?, ''), termination_error_code=NULLIF(?, '')
				WHERE run_id=? AND state IN ('starting', 'running')`,
				result.State, endedAt, result.ExitCode, result.StopReason,
				result.TerminationStatus, result.TerminationErrorCode, result.RunID)
		} else {
			updated, err = j.db.ExecContext(ctx, `UPDATE executions SET
				state=?, ended_at_unix_ns=?, exit_code=?, stop_reason=NULLIF(?, ''),
				termination_status=NULLIF(?, ''), termination_error_code=NULLIF(?, '')
				WHERE run_id=? AND state='running'`,
				result.State, endedAt, result.ExitCode, result.StopReason,
				result.TerminationStatus, result.TerminationErrorCode, result.RunID)
		}
	default:
		return fmt.Errorf("finish execution: %w", executions.ErrInvalidTransition)
	}
	return executionUpdateResult("finish execution", updated, err)
}

func validTerminationStatus(status executions.TerminationStatus) bool {
	switch status {
	case "", executions.TerminationNotRequired, executions.TerminationSucceeded, executions.TerminationFailed:
		return true
	default:
		return false
	}
}

func normalizedTerminationCode(code string) bool {
	if len(code) == 0 || len(code) > 64 || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	for i := 1; i < len(code); i++ {
		c := code[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func executionUpdateResult(action string, result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if count != 1 {
		return fmt.Errorf("%s: %w", action, executions.ErrInvalidTransition)
	}
	return nil
}

const executionColumns = `run_id, state, started_at_unix_ns, ended_at_unix_ns,
	exit_code, stop_reason, policy, policy_reason, policy_attempt, policy_threshold,
	blocked_call_estimate_usd, termination_status, termination_error_code`

// Execution returns one durable execution row.
func (j *Journal) Execution(ctx context.Context, runID string) (executions.Execution, error) {
	row := j.readDB.QueryRowContext(ctx,
		`SELECT `+executionColumns+` FROM executions WHERE run_id=?`, runID)
	got, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return executions.Execution{}, executions.ErrRunNotFound
		}
		return executions.Execution{}, fmt.Errorf("get execution: %w", err)
	}
	return got, nil
}

// ListExecutions returns the newest executions first.
func (j *Journal) ListExecutions(ctx context.Context, limit int) ([]executions.Execution, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list executions: limit must be positive")
	}
	rows, err := j.readDB.QueryContext(ctx,
		`SELECT `+executionColumns+` FROM executions ORDER BY started_at_unix_ns DESC, run_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()
	var out []executions.Execution
	for rows.Next() {
		got, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		out = append(out, got)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	return out, nil
}

type executionScanner interface{ Scan(...any) error }

func scanExecution(row executionScanner) (executions.Execution, error) {
	var got executions.Execution
	var started int64
	var ended, exitCode, attempt, threshold sql.NullInt64
	var stopReason, policy, policyReason, estimate, termination, terminationError sql.NullString
	err := row.Scan(&got.RunID, &got.State, &started, &ended, &exitCode,
		&stopReason, &policy, &policyReason, &attempt, &threshold, &estimate,
		&termination, &terminationError)
	if err != nil {
		return executions.Execution{}, err
	}
	got.StartedAt = time.Unix(0, started).UTC()
	if ended.Valid {
		t := time.Unix(0, ended.Int64).UTC()
		got.EndedAt = &t
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		got.ExitCode = &code
	}
	if attempt.Valid {
		value := attempt.Int64
		got.PolicyAttempt = &value
	}
	if threshold.Valid {
		value := threshold.Int64
		got.PolicyThreshold = &value
	}
	got.StopReason = stopReason.String
	got.Policy = policy.String
	got.PolicyReason = policyReason.String
	got.BlockedCallEstimateUSD = estimate.String
	got.TerminationStatus = executions.TerminationStatus(termination.String)
	got.TerminationErrorCode = terminationError.String
	return got, nil
}

// RecoverExecutions interrupts runs left active by a previous core process.
func (j *Journal) RecoverExecutions(ctx context.Context, recoveredAt time.Time) error {
	if recoveredAt.IsZero() {
		return fmt.Errorf("recover executions: invalid time")
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin execution recovery: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE executions SET
		state='interrupted', ended_at_unix_ns=?, stop_reason='virgil_restart'
		WHERE state IN ('starting', 'running')`, recoveredAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("recover executions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit execution recovery: %w", err)
	}
	return nil
}
