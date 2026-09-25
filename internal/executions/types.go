// Package executions tracks the identities and lifecycle of supervised runs.
package executions

import (
	"context"
	"errors"
	"time"
)

// State is the persisted lifecycle state of an execution.
type State string

const (
	StateStarting          State = "starting"
	StateRunning           State = "running"
	StateCompleted         State = "completed"
	StateFailed            State = "failed"
	StateBlocked           State = "blocked"
	StateDeadline          State = "deadline"
	StateInterrupted       State = "interrupted"
	StateGatewayFailed     State = "gateway_failed"
	StateTerminationFailed State = "termination_failed"
)

// TerminationStatus records whether the supervised process tree was stopped.
type TerminationStatus string

const (
	TerminationNotRequired TerminationStatus = "not_required"
	TerminationSucceeded   TerminationStatus = "succeeded"
	TerminationFailed      TerminationStatus = "failed"
)

// Registration is returned only to the local runner at registration time.
type Registration struct {
	RunID       string `json:"run_id"`
	RunToken    string `json:"run_token"`
	SignalToken string `json:"signal_token"`
}

// ExecutionResult is the runner's final lifecycle report.
type ExecutionResult struct {
	RunID                string            `json:"run_id"`
	State                State             `json:"state"`
	EndedAt              time.Time         `json:"ended_at"`
	ExitCode             int               `json:"exit_code"`
	StopReason           string            `json:"stop_reason,omitempty"`
	TerminationStatus    TerminationStatus `json:"termination_status,omitempty"`
	TerminationErrorCode string            `json:"termination_error_code,omitempty"`
}

// Execution is a durable lifecycle row. The command and environment are not stored.
type Execution struct {
	RunID                  string
	State                  State
	StartedAt              time.Time
	EndedAt                *time.Time
	ExitCode               *int
	StopReason             string
	Policy                 string
	PolicyReason           string
	PolicyAttempt          *int64
	PolicyThreshold        *int64
	BlockedCallEstimateUSD string
	TerminationStatus      TerminationStatus
	TerminationErrorCode   string
}

// PolicyBlockNotice carries the first policy decision that trips the circuit breaker.
type PolicyBlockNotice struct {
	RunID                  string    `json:"run_id"`
	Reason                 string    `json:"reason"`
	Policy                 string    `json:"policy"`
	Attempt                int64     `json:"attempt"`
	Threshold              int64     `json:"threshold"`
	BlockedCallEstimateUSD string    `json:"blocked_call_estimate_usd,omitempty"`
	OccurredAt             time.Time `json:"occurred_at"`
	PersistenceFailed      bool      `json:"persistence_failed,omitempty"`
}

// SignalKind identifies the message delivered to a supervised runner.
type SignalKind string

const SignalCircuitBreak SignalKind = "circuit_break"

// Signal is a one-shot notification sent through a run's signal stream.
type Signal struct {
	Kind        SignalKind
	PolicyBlock PolicyBlockNotice
}

// Store is the persistence contract needed for active execution lifecycles.
type Store interface {
	StartExecution(context.Context, string, time.Time) error
	MarkExecutionRunning(context.Context, string) error
	FinishExecution(context.Context, ExecutionResult) error
}

var (
	ErrInvalidRunID         = errors.New("invalid run id")
	ErrRunActive            = errors.New("run already active")
	ErrRunExists            = errors.New("run id already exists")
	ErrRunNotFound          = errors.New("run not found")
	ErrRunTokenInvalid      = errors.New("invalid run token")
	ErrSignalTokenInvalid   = errors.New("invalid signal token")
	ErrSignalAlreadyClaimed = errors.New("signal token already claimed")
	ErrInvalidTransition    = errors.New("invalid execution transition")
	ErrPolicyBlockRecorded  = errors.New("policy block already recorded")
)
