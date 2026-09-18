package policies

import (
	"context"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
)

func TestRemotePolicyCannotRaiseHardCap(t *testing.T) {
	local := Limits{
		MaxCallsPerRun:   100,
		MaxCostPerRunUSD: "1.00",
	}
	remote := controlplane.PolicyEnvelope{
		Version: 1,
		Limits: controlplane.CPLimits{
			MaxCallsPerRun:   200, // tries to raise the cap
			MaxCostPerRunUSD: "5.00",
		},
	}
	merged, err := Merge(local, remote, 0)
	if err != nil {
		t.Fatal(err)
	}
	if merged.MaxCallsPerRun != 100 {
		t.Fatalf("remote raised MaxCallsPerRun: got %d, want 100", merged.MaxCallsPerRun)
	}
	if merged.MaxCostPerRunUSD != "1.00" {
		t.Fatalf("remote raised MaxCostPerRunUSD: got %s, want 1.00", merged.MaxCostPerRunUSD)
	}
}

func TestDisjointRemoteAllowlistDeniesProvider(t *testing.T) {
	local := Limits{AllowedProviders: []string{"openai"}}
	remote := controlplane.PolicyEnvelope{Version: 1, Limits: controlplane.CPLimits{AllowedProviders: []string{"anthropic"}}}
	merged, err := Merge(local, remote, 0)
	if err != nil {
		t.Fatal(err)
	}
	engine := testEngine(t, merged)
	decision, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_disjoint", Provider: "openai", Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reason != "provider_not_allowed" {
		t.Fatalf("disjoint policy allowed provider: %+v", decision)
	}
}

func TestApplyRemotePolicyTightensActiveEngine(t *testing.T) {
	engine := testEngine(t, Limits{MaxCallsPerRun: 5})
	if err := engine.ApplyRemotePolicy(controlplane.PolicyEnvelope{Version: 1, Limits: controlplane.CPLimits{MaxCallsPerRun: 1}}); err != nil {
		t.Fatal(err)
	}
	first, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_remote", Provider: "p", Model: "m"})
	if err != nil || first.Decision != "allow" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := engine.Preflight(context.Background(), RequestFacts{RunID: "run_remote", Provider: "p", Model: "m"})
	if err != nil || second.Reason != "call_limit" {
		t.Fatalf("second: %+v %v", second, err)
	}
	if err := engine.ApplyRemotePolicy(controlplane.PolicyEnvelope{Version: 1, Limits: controlplane.CPLimits{MaxCallsPerRun: 100}}); err == nil {
		t.Fatal("stale policy accepted")
	}
}

func TestStalePolicyIgnored(t *testing.T) {
	local := Limits{MaxCallsPerRun: 10}
	stale := controlplane.PolicyEnvelope{Version: 1, Limits: controlplane.CPLimits{MaxCallsPerRun: 5}}
	_, err := Merge(local, stale, 2) // lastVersion=2, stale.Version=1
	if err == nil {
		t.Fatal("expected stale policy error")
	}
}

func TestMergeStricterAllowedList(t *testing.T) {
	local := Limits{AllowedProviders: []string{"anthropic", "openai"}}
	remote := controlplane.PolicyEnvelope{
		Version: 1,
		Limits:  controlplane.CPLimits{AllowedProviders: []string{"openai", "mistral"}},
	}
	merged, err := Merge(local, remote, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Intersection: only "openai" is in both
	if len(merged.AllowedProviders) != 1 || merged.AllowedProviders[0] != "openai" {
		t.Fatalf("expected intersection [openai], got %v", merged.AllowedProviders)
	}
}
