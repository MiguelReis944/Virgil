package policies

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestThreeErrorsBlockFourth(t *testing.T) {
	engine := testEngine(t, Limits{})
	for i := range 3 {
		decision, err := engine.RecordToolResult(context.Background(), ToolResult{
			RunID: "run_repeat", ToolCallID: "call_" + string(rune('a'+i)),
			ToolName: "lookup", Status: "error", ErrorCode: "timeout",
		})
		if err != nil || decision.Decision != "allow" {
			t.Fatalf("feedback %d: %+v %v", i, decision, err)
		}
	}
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_repeat", Provider: "fixture", Model: "fixture-model"})
	if err != nil || decision.Reason != "repeated_tool_error" || decision.Attempt != 4 || decision.Threshold != 3 {
		t.Fatalf("fourth: %+v %v", decision, err)
	}
}

func TestDifferentErrorResetsSequence(t *testing.T) {
	engine := testEngine(t, Limits{})
	for i, code := range []string{"timeout", "timeout", "invalid_output", "timeout"} {
		_, err := engine.RecordToolResult(context.Background(), ToolResult{
			RunID: "run_reset", ToolCallID: "call_" + string(rune('a'+i)),
			ToolName: "lookup", Status: "error", ErrorCode: code,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_reset", Provider: "fixture", Model: "fixture-model"})
	if err != nil || decision.Decision != "allow" {
		t.Fatalf("after reset: %+v %v", decision, err)
	}
}

func TestDuplicateToolResultDoesNotIncreaseSequence(t *testing.T) {
	engine := testEngine(t, Limits{})
	for range 4 {
		_, err := engine.RecordToolResult(context.Background(), ToolResult{
			RunID: "run_duplicate", ToolCallID: "same_call", ToolName: "lookup", Status: "error", ErrorCode: "timeout",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_duplicate", Provider: "fixture", Model: "fixture-model"})
	if err != nil || decision.Decision != "allow" {
		t.Fatalf("duplicate feedback: %+v %v", decision, err)
	}
}

func TestConcurrentToolResultsBlockAfterThree(t *testing.T) {
	engine := testEngine(t, Limits{})
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.RecordToolResult(context.Background(), ToolResult{
				RunID: "run_parallel", ToolCallID: fmt.Sprintf("call_%d", i),
				ToolName: "lookup", Status: "error", ErrorCode: "timeout",
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_parallel", Provider: "p", Model: "m"})
	if err != nil || decision.Reason != "repeated_tool_error" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestThreeIdenticalToolCallsBlockNextRequest(t *testing.T) {
	engine := testEngine(t, Limits{})
	for range 3 {
		if err := engine.RecordToolCalls(context.Background(), "run_calls", []string{strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_calls", Provider: "p", Model: "m"})
	if err != nil || decision.Reason != "repeated_tool_call" || decision.Attempt != 4 {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestDifferentToolCallResetsSequence(t *testing.T) {
	engine := testEngine(t, Limits{})
	for _, fingerprint := range []string{strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("a", 64)} {
		if err := engine.RecordToolCalls(context.Background(), "run_call_reset", []string{fingerprint}); err != nil {
			t.Fatal(err)
		}
	}
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_call_reset", Provider: "p", Model: "m"})
	if err != nil || decision.Decision != "allow" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}
