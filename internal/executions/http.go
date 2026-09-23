package executions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/controlauth"
)

const maxControlBody = 16 << 10

// HTTPStore reads durable execution summaries and applies the running transition.
type HTTPStore interface {
	MarkExecutionRunning(ctx context.Context, runID string) error
	Execution(ctx context.Context, runID string) (Execution, error)
}

// HTTPHandler exposes the installation-local execution control protocol.
type HTTPHandler struct {
	credential controlauth.Credential
	registry   *Registry
	store      HTTPStore
}

func NewHTTPHandler(credential controlauth.Credential, registry *Registry, store HTTPStore) *HTTPHandler {
	return &HTTPHandler{credential: credential, registry: registry, store: store}
}

type RegisterRequest struct {
	RunID string `json:"run_id,omitempty"`
}

type RunningRequest struct{}

type FinishRequest struct {
	State             State             `json:"state"`
	ExitCode          int               `json:"exit_code"`
	StopReason        string            `json:"stop_reason,omitempty"`
	TerminationStatus TerminationStatus `json:"termination_status,omitempty"`
	TerminationCode   string            `json:"termination_error_code,omitempty"`
}

func (h *HTTPHandler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || scheme != "Bearer" || !h.credential.Verify(token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func decodeControlJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeDecodeError(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeDecodeError(w, err)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid request body", http.StatusBadRequest)
}

func writeControlJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *HTTPHandler) Register(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(w, r) {
		return
	}
	var input RegisterRequest
	if !decodeControlJSON(w, r, &input) {
		return
	}
	registration, err := h.registry.Register(r.Context(), input.RunID)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidRunID):
			http.Error(w, "invalid run id", http.StatusBadRequest)
		case errors.Is(err, ErrRunActive):
			http.Error(w, "run already active", http.StatusConflict)
		default:
			http.Error(w, "registration failed", http.StatusInternalServerError)
		}
		return
	}
	writeControlJSON(w, http.StatusCreated, registration)
}

func (h *HTTPHandler) Running(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(w, r) {
		return
	}
	var input RunningRequest
	if !decodeControlJSON(w, r, &input) {
		return
	}
	runID := r.PathValue("run_id")
	if err := h.store.MarkExecutionRunning(r.Context(), runID); err != nil {
		if errors.Is(err, ErrInvalidTransition) {
			http.Error(w, "invalid transition", http.StatusConflict)
		} else {
			http.Error(w, "running transition failed", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) Finish(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(w, r) {
		return
	}
	var input FinishRequest
	if !decodeControlJSON(w, r, &input) {
		return
	}
	if !runnerTerminalState(input.State) {
		http.Error(w, "invalid terminal state", http.StatusBadRequest)
		return
	}
	if !validTerminationStatus(input.TerminationStatus) {
		http.Error(w, "invalid termination status", http.StatusBadRequest)
		return
	}
	runID := r.PathValue("run_id")
	before, err := h.store.Execution(r.Context(), runID)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			http.Error(w, "run not found", http.StatusNotFound)
		} else {
			http.Error(w, "execution read failed", http.StatusInternalServerError)
		}
		return
	}
	result := ExecutionResult{RunID: runID, State: input.State, EndedAt: time.Now().UTC(), ExitCode: input.ExitCode, StopReason: input.StopReason, TerminationStatus: input.TerminationStatus, TerminationErrorCode: input.TerminationCode}
	if before.State == StateBlocked {
		if !blockedFinishResult(input, &result) {
			http.Error(w, "invalid termination result", http.StatusBadRequest)
			return
		}
	} else if input.State == StateTerminationFailed {
		if input.TerminationStatus != TerminationFailed || input.TerminationCode == "" {
			http.Error(w, "invalid failed termination", http.StatusBadRequest)
			return
		}
	} else if input.TerminationStatus == TerminationFailed || input.TerminationCode != "" {
		http.Error(w, "invalid termination result", http.StatusBadRequest)
		return
	}
	err = h.registry.Finish(r.Context(), result)
	if errors.Is(err, ErrInvalidTransition) && before.State != StateBlocked {
		current, readErr := h.store.Execution(r.Context(), runID)
		if readErr != nil && !errors.Is(readErr, ErrRunNotFound) {
			http.Error(w, "execution read failed", http.StatusInternalServerError)
			return
		}
		if readErr == nil && current.State == StateBlocked {
			if !blockedFinishResult(input, &result) {
				http.Error(w, "invalid termination result", http.StatusBadRequest)
				return
			}
			err = h.registry.Finish(r.Context(), result)
		}
	}
	if err != nil {
		if errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrRunNotFound) {
			http.Error(w, "invalid transition", http.StatusConflict)
		} else {
			http.Error(w, "finish failed", http.StatusInternalServerError)
		}
		return
	}
	stored, err := h.store.Execution(r.Context(), runID)
	if err != nil {
		http.Error(w, "execution read failed", http.StatusInternalServerError)
		return
	}
	writeControlJSON(w, http.StatusOK, executionSummary(stored))
}

func blockedFinishResult(input FinishRequest, result *ExecutionResult) bool {
	switch input.TerminationStatus {
	case TerminationSucceeded:
		if input.TerminationCode != "" {
			return false
		}
		result.State = StateBlocked
		return true
	case TerminationFailed:
		if input.TerminationCode == "" {
			return false
		}
		result.State = StateTerminationFailed
		return true
	default:
		return false
	}
}

func validTerminationStatus(status TerminationStatus) bool {
	switch status {
	case "", TerminationNotRequired, TerminationSucceeded, TerminationFailed:
		return true
	default:
		return false
	}
}

func runnerTerminalState(state State) bool {
	switch state {
	case StateCompleted, StateFailed, StateDeadline, StateInterrupted, StateGatewayFailed, StateTerminationFailed:
		return true
	default:
		return false
	}
}

type summary struct {
	RunID                string            `json:"run_id"`
	State                State             `json:"state"`
	StartedAt            time.Time         `json:"started_at"`
	EndedAt              *time.Time        `json:"ended_at,omitempty"`
	ExitCode             *int              `json:"exit_code,omitempty"`
	StopReason           string            `json:"stop_reason,omitempty"`
	Policy               string            `json:"policy,omitempty"`
	PolicyReason         string            `json:"policy_reason,omitempty"`
	TerminationStatus    TerminationStatus `json:"termination_status,omitempty"`
	TerminationErrorCode string            `json:"termination_error_code,omitempty"`
}

func executionSummary(e Execution) summary {
	return summary{RunID: e.RunID, State: e.State, StartedAt: e.StartedAt, EndedAt: e.EndedAt, ExitCode: e.ExitCode, StopReason: e.StopReason, Policy: e.Policy, PolicyReason: e.PolicyReason, TerminationStatus: e.TerminationStatus, TerminationErrorCode: e.TerminationErrorCode}
}

func (h *HTTPHandler) Signals(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(w, r) {
		return
	}
	channel, release, err := h.registry.Subscribe(r.PathValue("run_id"), r.Header.Get("X-Virgil-Signal-Token"))
	if err != nil {
		if errors.Is(err, ErrSignalTokenInvalid) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "signal unavailable", http.StatusConflict)
		}
		return
	}
	defer release()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "event: ready\n\n")
	w.(http.Flusher).Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case signal, ok := <-channel:
			if !ok {
				return
			}
			if signal.Kind == SignalCircuitBreak {
				data, err := json.Marshal(signal.PolicyBlock)
				if err != nil {
					return
				}
				_, _ = io.WriteString(w, "event: circuit_break\ndata: "+string(data)+"\n\n")
				w.(http.Flusher).Flush()
				return
			}
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			w.(http.Flusher).Flush()
		}
	}
}
