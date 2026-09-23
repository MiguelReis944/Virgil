package executions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type activeRun struct {
	runTokenHash    [32]byte
	signalTokenHash [32]byte
	signals         chan Signal
	notifyOnce      sync.Once
	ready           bool
	claimed         bool
	closed          bool
	finishing       bool
}

// Registry holds only active, hashed credentials and one-shot signal streams.
type Registry struct {
	store Store
	mu    sync.RWMutex
	runs  map[string]*activeRun
}

func NewRegistry(store Store) *Registry {
	return &Registry{store: store, runs: make(map[string]*activeRun)}
}

func (r *Registry) Register(ctx context.Context, requestedRunID string) (Registration, error) {
	runID := requestedRunID
	if runID == "" {
		idBytes := make([]byte, 16)
		if _, err := rand.Read(idBytes); err != nil {
			return Registration{}, fmt.Errorf("generate run id: %w", err)
		}
		runID = "run_" + hex.EncodeToString(idBytes)
	} else if !validRunID(runID) {
		return Registration{}, ErrInvalidRunID
	}

	runToken, err := randomToken()
	if err != nil {
		return Registration{}, fmt.Errorf("generate run token: %w", err)
	}
	signalToken, err := randomToken()
	if err != nil {
		return Registration{}, fmt.Errorf("generate signal token: %w", err)
	}
	record := &activeRun{
		runTokenHash:    sha256.Sum256([]byte(runToken)),
		signalTokenHash: sha256.Sum256([]byte(signalToken)),
		signals:         make(chan Signal, 1),
	}

	r.mu.Lock()
	if _, exists := r.runs[runID]; exists {
		r.mu.Unlock()
		return Registration{}, ErrRunActive
	}
	r.runs[runID] = record
	r.mu.Unlock()

	if err := r.store.StartExecution(ctx, runID, time.Now().UTC()); err != nil {
		r.mu.Lock()
		delete(r.runs, runID)
		r.mu.Unlock()
		return Registration{}, fmt.Errorf("start execution: %w", err)
	}

	r.mu.Lock()
	record.ready = true
	r.mu.Unlock()
	return Registration{RunID: runID, RunToken: runToken, SignalToken: signalToken}, nil
}

func validRunID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (r *Registry) ResolveRunToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	hash := sha256.Sum256([]byte(token))
	r.mu.RLock()
	defer r.mu.RUnlock()
	for runID, record := range r.runs {
		if record.ready && !record.finishing && subtle.ConstantTimeCompare(hash[:], record.runTokenHash[:]) == 1 {
			return runID, true
		}
	}
	return "", false
}

// Subscribe claims a signal token once. The returned release function is idempotent.
func (r *Registry) Subscribe(runID, signalToken string) (<-chan Signal, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.runs[runID]
	if !exists || !record.ready || record.finishing {
		return nil, nil, ErrRunNotFound
	}
	hash := sha256.Sum256([]byte(signalToken))
	if signalToken == "" || subtle.ConstantTimeCompare(hash[:], record.signalTokenHash[:]) != 1 {
		return nil, nil, ErrSignalTokenInvalid
	}
	if record.claimed {
		return nil, nil, ErrSignalAlreadyClaimed
	}
	record.claimed = true
	release := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !record.closed {
			close(record.signals)
			record.closed = true
		}
	}
	return record.signals, release, nil
}

// NotifyPolicyBlock delivers at most one notice without waiting for a receiver.
func (r *Registry) NotifyPolicyBlock(notice PolicyBlockNotice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.runs[notice.RunID]
	if !exists || !record.ready || record.closed || record.finishing {
		return
	}
	record.notifyOnce.Do(func() {
		select {
		case record.signals <- Signal{Kind: SignalCircuitBreak, PolicyBlock: notice}:
		default:
		}
	})
}

// Finish persists a terminal result, then revokes credentials and ends the signal stream.
func (r *Registry) Finish(ctx context.Context, result ExecutionResult) error {
	r.mu.Lock()
	record, exists := r.runs[result.RunID]
	if !exists || !record.ready {
		r.mu.Unlock()
		return ErrRunNotFound
	}
	if record.finishing {
		r.mu.Unlock()
		return ErrInvalidTransition
	}
	record.finishing = true
	r.mu.Unlock()

	if err := r.store.FinishExecution(ctx, result); err != nil {
		r.mu.Lock()
		record.finishing = false
		r.mu.Unlock()
		return fmt.Errorf("finish execution: %w", err)
	}

	r.mu.Lock()
	if !record.closed {
		close(record.signals)
		record.closed = true
	}
	delete(r.runs, result.RunID)
	r.mu.Unlock()
	return nil
}
