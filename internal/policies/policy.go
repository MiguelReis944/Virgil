package policies

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

type Limits struct {
	MaxCallsPerRun        int64
	MaxCostPerRunUSD      string
	MaxInputTokensPerRun  int64
	MaxOutputTokensPerRun int64
	MaxTotalTokensPerRun  int64
	MaxDurationSeconds    int64
	MaxToolCallsPerRun    int64
	AllowedProviders      []string
	AllowedModels         []string
	AllowedTools          []string
}

type RequestFacts struct {
	RunID, Provider, Model                      string
	ToolNames                                   []string
	StartedAt                                   time.Time
	EstimatedCostUSD                            string
	EstimatedInputTokens, EstimatedOutputTokens *int64
}

type OutcomeFacts struct {
	RunID, ReservationID      string
	InputTokens, OutputTokens *int64
	CostUSD                   string
	ToolCalls                 *int64
}

type Decision struct {
	Decision      string `json:"decision"`
	Reason        string `json:"reason,omitempty"`
	Policy        string `json:"policy,omitempty"`
	Attempt       int64  `json:"attempt,omitempty"`
	Threshold     int64  `json:"threshold,omitempty"`
	RunID         string `json:"run_id"`
	ReservationID string `json:"-"`
}

type Engine struct {
	journal *storage.Journal
	limits  Limits
}

func NewEngine(journal *storage.Journal, limits Limits) (*Engine, error) {
	if journal == nil {
		return nil, errors.New("policy journal is required")
	}
	for _, v := range []int64{limits.MaxCallsPerRun, limits.MaxInputTokensPerRun, limits.MaxOutputTokensPerRun, limits.MaxTotalTokensPerRun, limits.MaxDurationSeconds, limits.MaxToolCallsPerRun} {
		if v < 0 {
			return nil, errors.New("negative policy limit")
		}
	}
	if _, err := decimal(limits.MaxCostPerRunUSD); err != nil {
		return nil, fmt.Errorf("cost cap: %w", err)
	}
	return &Engine{journal: journal, limits: limits}, nil
}

func allowed(list []string, value string) bool {
	if len(list) == 0 {
		return true
	}
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}

func block(runID, reason, policy string, attempt, threshold int64) Decision {
	return Decision{Decision: "block", Reason: reason, Policy: policy, RunID: runID, Attempt: attempt, Threshold: threshold}
}

func (e *Engine) Preflight(ctx context.Context, facts RequestFacts) (Decision, error) {
	runID, err := telemetry.ResolveRunID(facts.RunID)
	if err != nil {
		return Decision{}, err
	}
	if !allowed(e.limits.AllowedProviders, facts.Provider) {
		return block(runID, "provider_not_allowed", "allowed_providers", 0, 0), nil
	}
	if !allowed(e.limits.AllowedModels, facts.Model) {
		return block(runID, "model_not_allowed", "allowed_models", 0, 0), nil
	}
	for _, name := range facts.ToolNames {
		if !allowed(e.limits.AllowedTools, name) {
			return block(runID, "tool_not_allowed", "allowed_tools", 0, 0), nil
		}
	}
	started := facts.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if e.limits.MaxDurationSeconds > 0 && time.Since(started) > time.Duration(e.limits.MaxDurationSeconds)*time.Second {
		return block(runID, "duration_limit", "max_duration_seconds", 0, e.limits.MaxDurationSeconds), nil
	}
	if e.limits.MaxCostPerRunUSD != "" && facts.EstimatedCostUSD == "" {
		return block(runID, "cost_unavailable", "max_cost_per_run_usd", 0, 0), nil
	}
	if _, err := decimal(facts.EstimatedCostUSD); err != nil {
		return Decision{}, fmt.Errorf("cost estimate: %w", err)
	}
	input, output := int64(0), int64(0)
	if facts.EstimatedInputTokens != nil {
		input = *facts.EstimatedInputTokens
	}
	if facts.EstimatedOutputTokens != nil {
		output = *facts.EstimatedOutputTokens
	}
	if input < 0 || output < 0 {
		return Decision{}, errors.New("negative token estimate")
	}
	reservationID, err := telemetry.NewID(16)
	if err != nil {
		return Decision{}, err
	}
	r, err := e.journal.ReservePolicy(ctx, storage.PolicyReserve{
		RunID: runID, ReservationID: reservationID, StartedAt: started,
		InputTokens: input, OutputTokens: output, ToolCalls: int64(len(facts.ToolNames)), CostUSD: facts.EstimatedCostUSD,
		MaxCalls: e.limits.MaxCallsPerRun, MaxInputTokens: e.limits.MaxInputTokensPerRun,
		MaxOutputTokens: e.limits.MaxOutputTokensPerRun, MaxTotalTokens: e.limits.MaxTotalTokensPerRun,
		MaxToolCalls: e.limits.MaxToolCallsPerRun, MaxDurationSeconds: e.limits.MaxDurationSeconds,
		MaxCostUSD: e.limits.MaxCostPerRunUSD,
	})
	if err != nil {
		return Decision{}, err
	}
	if r.Reason != "" {
		return block(runID, r.Reason, r.Policy, r.Attempt, r.Threshold), nil
	}
	return Decision{Decision: "allow", RunID: runID, ReservationID: reservationID, Attempt: r.Attempt}, nil
}

func (e *Engine) Postflight(ctx context.Context, facts OutcomeFacts) error {
	if facts.ReservationID == "" {
		return errors.New("reservation ID is required")
	}
	input, output, tools := int64(-1), int64(-1), int64(-1)
	if facts.InputTokens != nil {
		input = *facts.InputTokens
	}
	if facts.OutputTokens != nil {
		output = *facts.OutputTokens
	}
	if facts.ToolCalls != nil {
		tools = *facts.ToolCalls
	}
	if input < -1 || output < -1 || tools < -1 {
		return errors.New("negative actual usage")
	}
	if _, err := decimal(facts.CostUSD); err != nil {
		return fmt.Errorf("actual cost: %w", err)
	}
	return e.journal.ReconcilePolicy(ctx, storage.PolicyOutcome{RunID: facts.RunID, ReservationID: facts.ReservationID, InputTokens: input, OutputTokens: output, ToolCalls: tools, CostUSD: facts.CostUSD})
}
