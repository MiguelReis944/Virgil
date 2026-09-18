package policies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
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
	denyProviders         bool
	denyModels            bool
	denyTools             bool
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
	journal             *storage.Journal
	limitsMu            sync.RWMutex
	localLimits         Limits
	limits              Limits
	lastRemoteVersion   int64
	repetitionMu        sync.Mutex
	repetitions         map[string]*repetitionState
	repetitionOrder     []string
	providerRepetitions map[string]*callRepetitionState
	providerOrder       []string
	callRepetitions     map[string]*callRepetitionState
	callOrder           []string
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
	return &Engine{
		journal: journal, localLimits: limits, limits: limits,
		repetitions:         make(map[string]*repetitionState),
		providerRepetitions: make(map[string]*callRepetitionState),
		callRepetitions:     make(map[string]*callRepetitionState),
	}, nil
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
	e.limitsMu.RLock()
	limits := e.limits
	e.limitsMu.RUnlock()
	runID, err := telemetry.ResolveRunID(facts.RunID)
	if err != nil {
		return Decision{}, err
	}
	if e.repeatedError(runID) {
		return block(runID, "repeated_tool_error", "repeated_error_limit", 4, 3), nil
	}
	if e.repeatedToolCall(runID) {
		return block(runID, "repeated_tool_call", "repeated_call_limit", 4, 3), nil
	}
	if limits.denyProviders || !allowed(limits.AllowedProviders, facts.Provider) {
		return block(runID, "provider_not_allowed", "allowed_providers", 0, 0), nil
	}
	if limits.denyModels || !allowed(limits.AllowedModels, facts.Model) {
		return block(runID, "model_not_allowed", "allowed_models", 0, 0), nil
	}
	for _, name := range facts.ToolNames {
		if limits.denyTools || !allowed(limits.AllowedTools, name) {
			return block(runID, "tool_not_allowed", "allowed_tools", 0, 0), nil
		}
	}
	started := facts.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if limits.MaxDurationSeconds > 0 && time.Since(started) > time.Duration(limits.MaxDurationSeconds)*time.Second {
		return block(runID, "duration_limit", "max_duration_seconds", 0, limits.MaxDurationSeconds), nil
	}
	if limits.MaxCostPerRunUSD != "" && facts.EstimatedCostUSD == "" {
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
	toolReservation := int64(0)
	if len(facts.ToolNames) > 0 {
		toolReservation = 1
	}
	r, err := e.journal.ReservePolicy(ctx, storage.PolicyReserve{
		RunID: runID, ReservationID: reservationID, StartedAt: started,
		InputTokens: input, OutputTokens: output, ToolCalls: toolReservation, CostUSD: facts.EstimatedCostUSD,
		MaxCalls: limits.MaxCallsPerRun, MaxInputTokens: limits.MaxInputTokensPerRun,
		MaxOutputTokens: limits.MaxOutputTokensPerRun, MaxTotalTokens: limits.MaxTotalTokensPerRun,
		MaxToolCalls: limits.MaxToolCallsPerRun, MaxDurationSeconds: limits.MaxDurationSeconds,
		MaxCostUSD: limits.MaxCostPerRunUSD,
	})
	if err != nil {
		return Decision{}, err
	}
	if r.Reason != "" {
		return block(runID, r.Reason, r.Policy, r.Attempt, r.Threshold), nil
	}
	return Decision{Decision: "allow", RunID: runID, ReservationID: reservationID, Attempt: r.Attempt}, nil
}

// ApplyRemotePolicy replaces the effective limits with a newer, stricter
// version while retaining the original local limits as the lower bound.
func (e *Engine) ApplyRemotePolicy(remote controlplane.PolicyEnvelope) error {
	if remote.Version <= 0 {
		return errors.New("remote policy version must be positive")
	}
	e.limitsMu.Lock()
	defer e.limitsMu.Unlock()
	merged, err := Merge(e.localLimits, remote, e.lastRemoteVersion)
	if err != nil {
		return err
	}
	e.limits = merged
	e.lastRemoteVersion = remote.Version
	return nil
}

func (e *Engine) RemoteVersion() int64 {
	e.limitsMu.RLock()
	defer e.limitsMu.RUnlock()
	return e.lastRemoteVersion
}

// CacheAndApplyRemotePolicy persists a validated policy before activating it.
func (e *Engine) CacheAndApplyRemotePolicy(ctx context.Context, remote controlplane.PolicyEnvelope) error {
	e.limitsMu.Lock()
	defer e.limitsMu.Unlock()
	merged, err := Merge(e.localLimits, remote, e.lastRemoteVersion)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(remote)
	if err != nil {
		return err
	}
	if err := e.journal.SaveRemotePolicy(ctx, remote.Version, payload); err != nil {
		return err
	}
	e.limits = merged
	e.lastRemoteVersion = remote.Version
	return nil
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

// Merge returns effective Limits by taking the stricter of local and remote.
// Remote cannot raise a local hard cap; endpoint and export fields are local-only.
// Returns an error if the remote version is stale (≤ lastVersion, when lastVersion > 0).
func Merge(local Limits, remote controlplane.PolicyEnvelope, lastVersion int64) (Limits, error) {
	if remote.Version <= 0 {
		return Limits{}, errors.New("remote policy version must be positive")
	}
	if lastVersion > 0 && remote.Version <= lastVersion {
		return Limits{}, fmt.Errorf("stale policy version %d (last seen %d)", remote.Version, lastVersion)
	}
	for _, v := range []int64{remote.Limits.MaxCallsPerRun, remote.Limits.MaxInputTokensPerRun, remote.Limits.MaxOutputTokensPerRun, remote.Limits.MaxTotalTokensPerRun, remote.Limits.MaxDurationSeconds, remote.Limits.MaxToolCallsPerRun} {
		if v < 0 {
			return Limits{}, errors.New("negative remote policy limit")
		}
	}
	if _, err := decimal(remote.Limits.MaxCostPerRunUSD); err != nil {
		return Limits{}, fmt.Errorf("invalid remote cost limit: %w", err)
	}
	result := local
	result.MaxCallsPerRun = stricterInt(local.MaxCallsPerRun, remote.Limits.MaxCallsPerRun)
	result.MaxInputTokensPerRun = stricterInt(local.MaxInputTokensPerRun, remote.Limits.MaxInputTokensPerRun)
	result.MaxOutputTokensPerRun = stricterInt(local.MaxOutputTokensPerRun, remote.Limits.MaxOutputTokensPerRun)
	result.MaxTotalTokensPerRun = stricterInt(local.MaxTotalTokensPerRun, remote.Limits.MaxTotalTokensPerRun)
	result.MaxDurationSeconds = stricterInt(local.MaxDurationSeconds, remote.Limits.MaxDurationSeconds)
	result.MaxToolCallsPerRun = stricterInt(local.MaxToolCallsPerRun, remote.Limits.MaxToolCallsPerRun)
	merged, err := stricterCost(local.MaxCostPerRunUSD, remote.Limits.MaxCostPerRunUSD)
	if err != nil {
		return Limits{}, fmt.Errorf("merge cost: %w", err)
	}
	result.MaxCostPerRunUSD = merged
	var deny bool
	result.AllowedProviders, deny = stricterList(local.AllowedProviders, remote.Limits.AllowedProviders)
	result.denyProviders = local.denyProviders || deny
	result.AllowedModels, deny = stricterList(local.AllowedModels, remote.Limits.AllowedModels)
	result.denyModels = local.denyModels || deny
	result.AllowedTools, deny = stricterList(local.AllowedTools, remote.Limits.AllowedTools)
	result.denyTools = local.denyTools || deny
	return result, nil
}

// stricterInt returns the smaller non-zero value; 0 means unlimited.
func stricterInt(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// stricterCost returns the smaller non-empty cost; empty means unlimited.
func stricterCost(a, b string) (string, error) {
	if a == "" {
		return b, nil
	}
	if b == "" {
		return a, nil
	}
	ra, ok1 := new(big.Rat).SetString(a)
	rb, ok2 := new(big.Rat).SetString(b)
	if !ok1 || !ok2 {
		return "", errors.New("invalid cost string")
	}
	if ra.Cmp(rb) <= 0 {
		return a, nil
	}
	return b, nil
}

// stricterList returns the intersection if both are non-empty; otherwise the non-empty one.
// An empty list means "all allowed".
func stricterList(local, remote []string) ([]string, bool) {
	if len(local) == 0 {
		return remote, false
	}
	if len(remote) == 0 {
		return local, false
	}
	remoteSet := make(map[string]bool, len(remote))
	for _, v := range remote {
		remoteSet[v] = true
	}
	var out []string
	for _, v := range local {
		if remoteSet[v] {
			out = append(out, v)
		}
	}
	return out, len(out) == 0
}
