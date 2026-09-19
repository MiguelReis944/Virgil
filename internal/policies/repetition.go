package policies

import (
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
)

const maxRepetitionRuns = 1024
const maxFeedbackIDsPerRun = 256

var feedbackRunID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var feedbackLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
var feedbackCode = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
var toolFingerprint = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ToolResult struct {
	RunID      string
	ToolCallID string
	ToolName   string
	Status     string
	ErrorCode  string
}

type repetitionState struct {
	fingerprint [32]byte
	count       int64
	seen        map[string]struct{}
	seenOrder   []string
}

type callRepetitionState struct {
	fingerprint string
	count       int64
}

func (e *Engine) RecordToolCalls(ctx context.Context, runID string, fingerprints []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !feedbackRunID.MatchString(runID) {
		return errors.New("invalid run ID")
	}
	for _, fp := range fingerprints {
		if !toolFingerprint.MatchString(fp) {
			return errors.New("invalid tool fingerprint")
		}
	}
	e.repetitionMu.Lock()
	for _, fp := range fingerprints {
		state := e.callRepetitions[runID]
		if state == nil {
			if len(e.callRepetitions) >= maxRepetitionRuns {
				delete(e.callRepetitions, e.callOrder[0])
				e.callOrder = e.callOrder[1:]
			}
			state = &callRepetitionState{}
			e.callRepetitions[runID] = state
			e.callOrder = append(e.callOrder, runID)
		}
		if state.fingerprint == fp {
			state.count++
		} else {
			state.fingerprint = fp
			state.count = 1
		}
	}
	var saveCount int64
	var saveFingerprint string
	if s := e.callRepetitions[runID]; s != nil {
		saveCount = s.count
		saveFingerprint = s.fingerprint
	}
	e.repetitionMu.Unlock()
	// Persist outside mutex — best-effort; restart restores from here.
	if e.journal != nil && saveFingerprint != "" {
		_ = e.journal.SaveRepetitionState(ctx, runID, "tool_call", saveCount, []string{saveFingerprint})
	}
	return nil
}

// repeatedToolCall checks whether the same tool fingerprint has been seen ≥3 times.
// On cache miss it lazy-loads from SQLite.
func (e *Engine) repeatedToolCall(ctx context.Context, runID string) bool {
	e.repetitionMu.Lock()
	s := e.callRepetitions[runID]
	e.repetitionMu.Unlock()
	if s != nil {
		return s.count >= 3
	}
	// Cache miss — try SQLite.
	if e.journal == nil {
		return false
	}
	count, vals, err := e.journal.LoadRepetitionState(ctx, runID, "tool_call")
	if err != nil || count == 0 {
		return false
	}
	fp := ""
	if len(vals) > 0 {
		fp = vals[0]
	}
	e.repetitionMu.Lock()
	if e.callRepetitions[runID] == nil {
		if len(e.callRepetitions) >= maxRepetitionRuns {
			delete(e.callRepetitions, e.callOrder[0])
			e.callOrder = e.callOrder[1:]
		}
		e.callRepetitions[runID] = &callRepetitionState{fingerprint: fp, count: count}
		e.callOrder = append(e.callOrder, runID)
	}
	e.repetitionMu.Unlock()
	return count >= 3
}

func (e *Engine) RecordToolResult(ctx context.Context, result ToolResult) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if !feedbackRunID.MatchString(result.RunID) || !feedbackLabel.MatchString(result.ToolCallID) ||
		!feedbackLabel.MatchString(result.ToolName) ||
		(result.Status != "error" && result.Status != "success") ||
		(result.Status == "error" && !feedbackCode.MatchString(result.ErrorCode)) ||
		(result.Status == "success" && result.ErrorCode != "") {
		return Decision{}, errors.New("invalid tool result metadata")
	}
	fingerprint := sha256.Sum256([]byte(result.ToolName + "\x00" + result.Status + "\x00" + result.ErrorCode))
	e.repetitionMu.Lock()
	state := e.repetitions[result.RunID]
	if state == nil {
		if len(e.repetitions) >= maxRepetitionRuns {
			delete(e.repetitions, e.repetitionOrder[0])
			e.repetitionOrder = e.repetitionOrder[1:]
		}
		state = &repetitionState{seen: make(map[string]struct{})}
		e.repetitions[result.RunID] = state
		e.repetitionOrder = append(e.repetitionOrder, result.RunID)
	}
	if _, duplicate := state.seen[result.ToolCallID]; duplicate {
		e.repetitionMu.Unlock()
		return Decision{Decision: "allow", RunID: result.RunID}, nil
	}
	if len(state.seen) >= maxFeedbackIDsPerRun {
		delete(state.seen, state.seenOrder[0])
		state.seenOrder = state.seenOrder[1:]
	}
	state.seen[result.ToolCallID] = struct{}{}
	state.seenOrder = append(state.seenOrder, result.ToolCallID)
	if result.Status == "success" {
		state.count = 0
	} else if state.fingerprint == fingerprint {
		state.count++
	} else {
		state.count = 1
	}
	state.fingerprint = fingerprint
	saveCount := state.count
	e.repetitionMu.Unlock()
	// Persist outside mutex.
	if e.journal != nil {
		_ = e.journal.SaveRepetitionState(ctx, result.RunID, "tool_error", saveCount, []string{result.ToolName, result.ErrorCode})
	}
	return Decision{Decision: "allow", RunID: result.RunID}, nil
}

func (e *Engine) RecordProviderOutcome(ctx context.Context, runID, status, errorCode string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !feedbackRunID.MatchString(runID) ||
		(status != "success" && !feedbackCode.MatchString(errorCode)) {
		return errors.New("invalid provider outcome")
	}
	e.repetitionMu.Lock()
	state := e.providerRepetitions[runID]
	if state == nil {
		if len(e.providerRepetitions) >= maxRepetitionRuns {
			delete(e.providerRepetitions, e.providerOrder[0])
			e.providerOrder = e.providerOrder[1:]
		}
		state = &callRepetitionState{}
		e.providerRepetitions[runID] = state
		e.providerOrder = append(e.providerOrder, runID)
	}
	if status == "success" {
		state.count = 0
		state.fingerprint = ""
	} else if state.fingerprint == errorCode {
		state.count++
	} else {
		state.count = 1
		state.fingerprint = errorCode
	}
	saveCount := state.count
	saveFingerprint := state.fingerprint
	e.repetitionMu.Unlock()
	// Persist outside mutex.
	if e.journal != nil {
		_ = e.journal.SaveRepetitionState(ctx, runID, "provider_error", saveCount, []string{saveFingerprint})
	}
	return nil
}

// repeatedToolError lazy-loads from SQLite on cache miss.
func (e *Engine) repeatedToolError(ctx context.Context, runID string) bool {
	e.repetitionMu.Lock()
	s := e.repetitions[runID]
	e.repetitionMu.Unlock()
	if s != nil {
		return s.count >= 3
	}
	if e.journal == nil {
		return false
	}
	count, _, err := e.journal.LoadRepetitionState(ctx, runID, "tool_error")
	if err != nil || count == 0 {
		return false
	}
	e.repetitionMu.Lock()
	if e.repetitions[runID] == nil {
		if len(e.repetitions) >= maxRepetitionRuns {
			delete(e.repetitions, e.repetitionOrder[0])
			e.repetitionOrder = e.repetitionOrder[1:]
		}
		e.repetitions[runID] = &repetitionState{count: count, seen: make(map[string]struct{})}
		e.repetitionOrder = append(e.repetitionOrder, runID)
	}
	e.repetitionMu.Unlock()
	return count >= 3
}

// repeatedProviderError lazy-loads from SQLite on cache miss.
func (e *Engine) repeatedProviderError(ctx context.Context, runID string) bool {
	e.repetitionMu.Lock()
	s := e.providerRepetitions[runID]
	e.repetitionMu.Unlock()
	if s != nil {
		return s.count >= 3
	}
	if e.journal == nil {
		return false
	}
	count, vals, err := e.journal.LoadRepetitionState(ctx, runID, "provider_error")
	if err != nil || count == 0 {
		return false
	}
	fp := ""
	if len(vals) > 0 {
		fp = vals[0]
	}
	e.repetitionMu.Lock()
	if e.providerRepetitions[runID] == nil {
		if len(e.providerRepetitions) >= maxRepetitionRuns {
			delete(e.providerRepetitions, e.providerOrder[0])
			e.providerOrder = e.providerOrder[1:]
		}
		e.providerRepetitions[runID] = &callRepetitionState{fingerprint: fp, count: count}
		e.providerOrder = append(e.providerOrder, runID)
	}
	e.repetitionMu.Unlock()
	return count >= 3
}

// repeatedError is kept for any callers outside Preflight.
func (e *Engine) repeatedError(ctx context.Context, runID string) bool {
	return e.repeatedToolError(ctx, runID) || e.repeatedProviderError(ctx, runID)
}
