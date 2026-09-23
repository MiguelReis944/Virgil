package executions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu        sync.Mutex
	started   map[string]time.Time
	finished  map[string]ExecutionResult
	startErr  error
	finishErr error
}

func (s *fakeStore) StartExecution(_ context.Context, runID string, startedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return s.startErr
	}
	if s.started == nil {
		s.started = make(map[string]time.Time)
	}
	s.started[runID] = startedAt
	return nil
}

func (s *fakeStore) MarkExecutionRunning(context.Context, string) error { return nil }

func (s *fakeStore) FinishExecution(_ context.Context, result ExecutionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finishErr != nil {
		return s.finishErr
	}
	if s.finished == nil {
		s.finished = make(map[string]ExecutionResult)
	}
	s.finished[result.RunID] = result
	return nil
}

func TestRegistryRegisterGeneratedAndRequestedIDs(t *testing.T) {
	store := &fakeStore{}
	r := NewRegistry(store)
	requested, err := r.Register(context.Background(), "run_one.2")
	if err != nil {
		t.Fatal(err)
	}
	if requested.RunID != "run_one.2" || requested.RunToken == "" || requested.SignalToken == "" {
		t.Fatalf("invalid requested registration: %+v", requested)
	}
	generated, err := r.Register(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if generated.RunID == "" || len(generated.RunID) > 64 || generated.RunID == requested.RunID {
		t.Fatalf("invalid generated run ID: %q", generated.RunID)
	}
	if generated.RunToken == requested.RunToken || generated.SignalToken == requested.SignalToken || generated.RunToken == generated.SignalToken {
		t.Fatal("tokens were reused")
	}
	store.mu.Lock()
	_, requestedStarted := store.started[requested.RunID]
	_, generatedStarted := store.started[generated.RunID]
	store.mu.Unlock()
	if !requestedStarted || !generatedStarted {
		t.Fatal("registrations were not persisted")
	}
}

func TestRegistryRegisterRejectsInvalidAndActiveIDs(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	invalid := []string{"has space", "slash/id", "é", "a\n", string(make([]byte, 65))}
	for _, id := range invalid {
		if _, err := r.Register(context.Background(), id); !errors.Is(err, ErrInvalidRunID) {
			t.Errorf("Register(%q) error = %v, want ErrInvalidRunID", id, err)
		}
	}
	if _, err := r.Register(context.Background(), "same"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(context.Background(), "same"); !errors.Is(err, ErrRunActive) {
		t.Fatalf("duplicate registration error = %v, want ErrRunActive", err)
	}
}

func TestRegistryRegisterFailureReleasesPendingID(t *testing.T) {
	store := &fakeStore{startErr: errors.New("disk unavailable")}
	r := NewRegistry(store)
	if _, err := r.Register(context.Background(), "retry"); err == nil {
		t.Fatal("expected persistence failure")
	}
	store.mu.Lock()
	store.startErr = nil
	store.mu.Unlock()
	if _, err := r.Register(context.Background(), "retry"); err != nil {
		t.Fatalf("pending run was retained: %v", err)
	}
}

func TestRegistryResolveRunTokenBindsIdentity(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	a, err := r.Register(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Register(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, want string
		ok                bool
	}{
		{"first", a.RunToken, "a", true},
		{"second", b.RunToken, "b", true},
		{"signal token is isolated", a.SignalToken, "", false},
		{"empty", "", "", false},
		{"wrong", "not-a-token", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := r.ResolveRunToken(tc.token)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ResolveRunToken() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRegistrySubscribeClaimsSignalTokenOnce(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	a, err := r.Register(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Register(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Subscribe("a", b.SignalToken); !errors.Is(err, ErrSignalTokenInvalid) {
		t.Fatalf("cross-run signal token error = %v", err)
	}
	if _, _, err := r.Subscribe("a", a.RunToken); !errors.Is(err, ErrSignalTokenInvalid) {
		t.Fatalf("run token accepted as signal token: %v", err)
	}
	if _, _, err := r.Subscribe("missing", a.SignalToken); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("missing run error = %v", err)
	}
	_, cancel, err := r.Subscribe("a", a.SignalToken)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	if _, _, err := r.Subscribe("a", a.SignalToken); !errors.Is(err, ErrSignalAlreadyClaimed) {
		t.Fatalf("reused signal token error = %v", err)
	}
}

func TestRegistryConcurrentSignalClaims(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	registration, err := r.Register(context.Background(), "claim")
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for range workers {
		wg.Go(func() {
			_, cancel, err := r.Subscribe("claim", registration.SignalToken)
			if err != nil {
				if !errors.Is(err, ErrSignalAlreadyClaimed) {
					t.Errorf("claim error = %v", err)
				}
				return
			}
			mu.Lock()
			successes++
			mu.Unlock()
			cancel()
		})
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
}

func TestRegistryNotifyPolicyBlockOnceAndWithoutSubscriber(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	registration, err := r.Register(context.Background(), "blocked")
	if err != nil {
		t.Fatal(err)
	}
	first := PolicyBlockNotice{RunID: "blocked", Reason: "limit", Policy: "max_calls", Attempt: 2}
	r.NotifyPolicyBlock(first)
	ch, cancel, err := r.Subscribe("blocked", registration.SignalToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	r.NotifyPolicyBlock(PolicyBlockNotice{RunID: "blocked", Reason: "later"})
	select {
	case signal := <-ch:
		if signal.Kind != SignalCircuitBreak || signal.PolicyBlock != first {
			t.Fatalf("signal = %+v, want first block", signal)
		}
	case <-time.After(time.Second):
		t.Fatal("block signal not delivered")
	}
	select {
	case signal := <-ch:
		t.Fatalf("extra signal: %+v", signal)
	default:
	}
}

func TestRegistryFinishRevokesTokensAndClosesSignal(t *testing.T) {
	store := &fakeStore{}
	r := NewRegistry(store)
	registration, err := r.Register(context.Background(), "done")
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel, err := r.Subscribe("done", registration.SignalToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	result := ExecutionResult{RunID: "done", State: StateCompleted, ExitCode: 0}
	if err := r.Finish(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ResolveRunToken(registration.RunToken); ok {
		t.Fatal("finished token still accepted")
	}
	if _, ok := <-ch; ok {
		t.Fatal("finished signal stream remains open")
	}
	if _, _, err := r.Subscribe("done", registration.SignalToken); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("subscribe after finish error = %v", err)
	}
	store.mu.Lock()
	got := store.finished["done"]
	store.mu.Unlock()
	if got != result {
		t.Fatalf("stored result = %+v, want %+v", got, result)
	}
}

func TestRegistryFinishRetainsRunOnStoreFailure(t *testing.T) {
	store := &fakeStore{finishErr: errors.New("disk unavailable")}
	r := NewRegistry(store)
	registration, err := r.Register(context.Background(), "retry")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Finish(context.Background(), ExecutionResult{RunID: "retry", State: StateFailed}); err == nil {
		t.Fatal("expected finish error")
	}
	if _, ok := r.ResolveRunToken(registration.RunToken); !ok {
		t.Fatal("failed persistence revoked token")
	}
}

func TestRegistryConcurrentRegistrationAndNotification(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	const workers = 64
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for range workers {
		wg.Go(func() {
			registration, err := r.Register(context.Background(), "same")
			if err != nil {
				if !errors.Is(err, ErrRunActive) {
					t.Errorf("registration error = %v", err)
				}
				return
			}
			mu.Lock()
			successes++
			mu.Unlock()
			ch, cancel, err := r.Subscribe("same", registration.SignalToken)
			if err != nil {
				t.Error(err)
				return
			}
			defer cancel()
			var notices sync.WaitGroup
			for range workers {
				notices.Go(func() { r.NotifyPolicyBlock(PolicyBlockNotice{RunID: "same", Reason: "limit"}) })
			}
			notices.Wait()
			if signal := <-ch; signal.Kind != SignalCircuitBreak {
				t.Errorf("signal = %+v", signal)
			}
			select {
			case <-ch:
				t.Error("duplicate notification")
			default:
			}
		})
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("registrations = %d, want 1", successes)
	}
}

func TestRegistryNotifyRacingFinish(t *testing.T) {
	r := NewRegistry(&fakeStore{})
	registration, err := r.Register(context.Background(), "race")
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel, err := r.Subscribe("race", registration.SignalToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	var wg sync.WaitGroup
	for range 128 {
		wg.Go(func() { r.NotifyPolicyBlock(PolicyBlockNotice{RunID: "race"}) })
	}
	wg.Go(func() {
		if err := r.Finish(context.Background(), ExecutionResult{RunID: "race", State: StateCompleted}); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	for range ch {
	}
	if _, ok := r.ResolveRunToken(registration.RunToken); ok {
		t.Fatal("run token accepted after finish")
	}
}
