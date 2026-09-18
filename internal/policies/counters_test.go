package policies

import (
	"context"
	"testing"
)

func TestPostflightReconcilesUsage(t *testing.T) {
	e := testEngine(t, Limits{MaxCallsPerRun: 3, MaxInputTokensPerRun: 5, MaxCostPerRunUSD: "1.00"})
	est := int64(4)
	d, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_reconcile", Provider: "p", Model: "m", EstimatedInputTokens: &est, EstimatedCostUSD: "0.80"})
	if err != nil || d.Decision != "allow" {
		t.Fatalf("preflight: %+v %v", d, err)
	}
	actual := int64(2)
	if err := e.Postflight(context.Background(), OutcomeFacts{RunID: d.RunID, ReservationID: d.ReservationID, InputTokens: &actual, CostUSD: "0.25"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Postflight(context.Background(), OutcomeFacts{RunID: d.RunID, ReservationID: d.ReservationID, InputTokens: &actual, CostUSD: "0.25"}); err != nil {
		t.Fatal(err)
	}
	d, err = e.Preflight(context.Background(), RequestFacts{RunID: "run_reconcile", Provider: "p", Model: "m", EstimatedInputTokens: &est, EstimatedCostUSD: "0.70"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Decision != "block" || d.Reason != "input_token_limit" {
		t.Fatalf("got %+v", d)
	}
	est = 3
	d, err = e.Preflight(context.Background(), RequestFacts{RunID: "run_reconcile", Provider: "p", Model: "m", EstimatedInputTokens: &est, EstimatedCostUSD: "0.70"})
	if err != nil || d.Decision != "allow" {
		t.Fatalf("after reconcile: %+v %v", d, err)
	}
}

func TestCostAndToolLimits(t *testing.T) {
	e := testEngine(t, Limits{MaxCostPerRunUSD: "0.50", MaxToolCallsPerRun: 1})
	first, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_cost", Provider: "p", Model: "m", EstimatedCostUSD: "0.40", ToolNames: []string{"one"}})
	if err != nil || first.Decision != "allow" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_cost", Provider: "p", Model: "m", EstimatedCostUSD: "0.20"})
	if err != nil || second.Reason != "cost_limit" {
		t.Fatalf("cost: %+v %v", second, err)
	}
	third, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_cost", Provider: "p", Model: "m", EstimatedCostUSD: "0.10", ToolNames: []string{"two"}})
	if err != nil || third.Reason != "tool_call_limit" {
		t.Fatalf("tool: %+v %v", third, err)
	}
}

func TestDeclaredToolsReserveOnlyOnePossibleCall(t *testing.T) {
	e := testEngine(t, Limits{MaxToolCallsPerRun: 1})
	d, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_declared", Provider: "p", Model: "m", ToolNames: []string{"one", "two", "three"}})
	if err != nil || d.Decision != "allow" {
		t.Fatalf("declaring tools blocked request: %+v %v", d, err)
	}
	zero := int64(0)
	if err := e.Postflight(context.Background(), OutcomeFacts{RunID: d.RunID, ReservationID: d.ReservationID, ToolCalls: &zero}); err != nil {
		t.Fatal(err)
	}
	d, err = e.Preflight(context.Background(), RequestFacts{RunID: "run_declared", Provider: "p", Model: "m", ToolNames: []string{"one"}})
	if err != nil || d.Decision != "allow" {
		t.Fatalf("unused declarations consumed budget: %+v %v", d, err)
	}
}

func TestTotalTokenLimit(t *testing.T) {
	e := testEngine(t, Limits{MaxTotalTokensPerRun: 5})
	in, out := int64(3), int64(3)
	d, err := e.Preflight(context.Background(), RequestFacts{RunID: "run_total", Provider: "p", Model: "m", EstimatedInputTokens: &in, EstimatedOutputTokens: &out})
	if err != nil || d.Reason != "total_token_limit" {
		t.Fatalf("total: %+v %v", d, err)
	}
}
