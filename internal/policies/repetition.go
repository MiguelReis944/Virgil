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
	for _, fingerprint := range fingerprints {
		if !toolFingerprint.MatchString(fingerprint) {
			return errors.New("invalid tool fingerprint")
		}
	}
	e.repetitionMu.Lock()
	defer e.repetitionMu.Unlock()
	for _, fingerprint := range fingerprints {
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
		if state.fingerprint == fingerprint {
			state.count++
		} else {
			state.fingerprint = fingerprint
			state.count = 1
		}
	}
	return nil
}

func (e *Engine) repeatedToolCall(runID string) bool {
	e.repetitionMu.Lock()
	defer e.repetitionMu.Unlock()
	return e.callRepetitions[runID] != nil && e.callRepetitions[runID].count >= 3
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
	defer e.repetitionMu.Unlock()
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
	defer e.repetitionMu.Unlock()
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
	return nil
}

func (e *Engine) repeatedError(runID string) bool {
	e.repetitionMu.Lock()
	defer e.repetitionMu.Unlock()
	return (e.repetitions[runID] != nil && e.repetitions[runID].count >= 3) ||
		(e.providerRepetitions[runID] != nil && e.providerRepetitions[runID].count >= 3)
}
