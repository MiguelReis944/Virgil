package policies

import (
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
