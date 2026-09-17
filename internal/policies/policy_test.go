package policies

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

func testEngine(t *testing.T, limits Limits) *Engine {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	j, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(j.Close)
	e, err := NewEngine(j, limits)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestConcurrentLastCallBudgetAllowsOne(t *testing.T) {
	e := testEngine(t, Limits{MaxCallsPerRun: 1})
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan Decision, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_same", Provider: "test", Model: "test"})
			if err != nil {
				t.Error(err)
			}
			results <- d
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	allows := 0
	for d := range results {
		if d.Decision == "allow" {
			allows++
		} else if d.Reason != "call_limit" || d.Attempt != 2 || d.Threshold != 1 {
			t.Errorf("unexpected block: %+v", d)
		}
	}
	if allows != 1 {
		t.Fatalf("allows=%d, want 1", allows)
	}
}

func TestAllowedProviderModelTool(t *testing.T) {
	e := testEngine(t, Limits{AllowedProviders: []string{"one"}, AllowedModels: []string{"model"}, AllowedTools: []string{"safe"}})
	for _, tc := range []struct {
		name   string
		facts  RequestFacts
		reason string
	}{
		{"provider", RequestFacts{RunID: "r", Provider: "two", Model: "model"}, "provider_not_allowed"},
		{"model", RequestFacts{RunID: "r", Provider: "one", Model: "other"}, "model_not_allowed"},
		{"tool", RequestFacts{RunID: "r", Provider: "one", Model: "model", ToolNames: []string{"unsafe"}}, "tool_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := e.Preflight(context.Background(), tc.facts)
			if err != nil {
				t.Fatal(err)
			}
			if d.Decision != "block" || d.Reason != tc.reason || d.Policy == "" {
				t.Fatalf("decision=%+v", d)
			}
		})
	}
}

func TestDurationAndTokenLimits(t *testing.T) {
	in := int64(3)
	out := int64(4)
	e := testEngine(t, Limits{MaxDurationSeconds: 1, MaxInputTokensPerRun: 2, MaxOutputTokensPerRun: 3, MaxTotalTokensPerRun: 4})
	for _, tc := range []struct {
		name   string
		facts  RequestFacts
		reason string
	}{
		{"duration", RequestFacts{RunID: "a", Provider: "p", Model: "m", StartedAt: time.Now().Add(-2 * time.Second)}, "duration_limit"},
		{"input", RequestFacts{RunID: "b", Provider: "p", Model: "m", EstimatedInputTokens: &in}, "input_token_limit"},
		{"output", RequestFacts{RunID: "c", Provider: "p", Model: "m", EstimatedOutputTokens: &out}, "output_token_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := e.Preflight(context.Background(), tc.facts)
			if err != nil {
				t.Fatal(err)
			}
			if d.Reason != tc.reason {
				t.Fatalf("got %+v", d)
			}
		})
	}
}

func TestCostUnavailableAndGeneratedRunID(t *testing.T) {
	e := testEngine(t, Limits{MaxCostPerRunUSD: "1.00"})
	d, err := e.Preflight(context.Background(), RequestFacts{RunID: "invalid id!", Provider: "p", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Decision != "block" || d.Reason != "cost_unavailable" || d.RunID == "invalid id!" || d.RunID == "" {
		t.Fatalf("got %+v", d)
	}
}

func TestCallBudgetSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.db")
	open := func() (*Engine, func()) {
		db, err := storage.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		journal, err := storage.NewJournal(db)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		engine, err := NewEngine(journal, Limits{MaxCallsPerRun: 1})
		if err != nil {
			journal.Close()
			db.Close()
			t.Fatal(err)
		}
		return engine, func() { journal.Close(); db.Close() }
	}
	engine, closeFirst := open()
	first, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_restart", Provider: "p", Model: "m"})
	if err != nil || first.Decision != "allow" {
		t.Fatalf("first: %+v %v", first, err)
	}
	closeFirst()
	engine, closeSecond := open()
	defer closeSecond()
	second, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_restart", Provider: "p", Model: "m"})
	if err != nil || second.Decision != "block" || second.Reason != "call_limit" || second.Attempt != 2 {
		t.Fatalf("after restart: %+v %v", second, err)
	}
}

func TestUnknownOutcomeKeepsConservativeReservation(t *testing.T) {
	engine := testEngine(t, Limits{MaxInputTokensPerRun: 4, MaxCostPerRunUSD: "0.50"})
	estimate := int64(3)
	first, err := engine.Preflight(context.Background(), RequestFacts{
		RunID: "run_unknown", Provider: "p", Model: "m", EstimatedInputTokens: &estimate, EstimatedCostUSD: "0.40",
	})
	if err != nil || first.Decision != "allow" {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err := engine.Postflight(context.Background(), OutcomeFacts{RunID: first.RunID, ReservationID: first.ReservationID}); err != nil {
		t.Fatal(err)
	}
	second, err := engine.Preflight(context.Background(), RequestFacts{
		RunID: "run_unknown", Provider: "p", Model: "m", EstimatedInputTokens: &estimate, EstimatedCostUSD: "0.20",
	})
	if err != nil || second.Decision != "block" || second.Reason != "input_token_limit" {
		t.Fatalf("unknown outcome: %+v %v", second, err)
	}
}
