package pricing

import (
	"fmt"
	"math/big"
	"strings"
	"sync"
)

// ModelPriceSource is the minimal interface the registry needs from the config's
// pricing section, avoiding a direct import of the config package.
type ModelPriceSource struct {
	Model          string
	InputPerToken  string
	OutputPerToken string
	CachedPerToken string
}

// NewPriceRegistryFromEntries builds a Registry from a slice of ModelPriceSource.
// Each entry is registered with an empty provider, making it available for any provider.
func NewPriceRegistryFromEntries(entries []ModelPriceSource) *Registry {
	r := NewPriceRegistry()
	for _, e := range entries {
		r.Set(PriceEntry{
			Provider:       "",
			Model:          e.Model,
			InputPerToken:  e.InputPerToken,
			OutputPerToken: e.OutputPerToken,
			CachedPerToken: e.CachedPerToken,
		})
	}
	return r
}

// PriceEntry holds input/output cost per token for a specific provider+model combination.
// Prices are decimal strings denominated in the table's Currency per token.
type PriceEntry struct {
	Provider        string
	Model           string
	InputPerToken   string // decimal string, e.g. "0.000003"
	OutputPerToken  string
	CachedPerToken  string // optional; applies to cached input tokens
}

// Registry is a thread-safe table of per-model pricing keyed by "provider:model".
// An empty provider string means the entry applies to any provider using that model.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]PriceEntry // key: "provider:model" (lowercased)
}

// NewPriceRegistry returns an empty Registry.
func NewPriceRegistry() *Registry {
	return &Registry{entries: make(map[string]PriceEntry)}
}

// Set adds or replaces an entry. The key is "provider:model" (case-insensitive).
func (r *Registry) Set(e PriceEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[strings.ToLower(e.Provider+":"+e.Model)] = e
}

// Lookup returns the PriceEntry for a provider+model pair.
// It first tries "provider:model", then falls back to ":model" (provider-agnostic).
func (r *Registry) Lookup(provider, model string) (PriceEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[strings.ToLower(provider+":"+model)]; ok {
		return e, true
	}
	// Fall back to provider-agnostic entry.
	e, ok := r.entries[strings.ToLower(":"+model)]
	return e, ok
}

// EstimateCostUSD returns the estimated cost string for a given token count.
// Uses big.Rat arithmetic for precision.
func (r *Registry) EstimateCostUSD(provider, model string, inputTokens, outputTokens int64) (string, error) {
	e, ok := r.Lookup(provider, model)
	if !ok {
		return "", fmt.Errorf("no pricing for %s/%s", provider, model)
	}
	inputRate, ok1 := new(big.Rat).SetString(e.InputPerToken)
	outputRate, ok2 := new(big.Rat).SetString(e.OutputPerToken)
	if !ok1 || !ok2 {
		return "", fmt.Errorf("invalid pricing rates for %s/%s", provider, model)
	}
	inputCost := new(big.Rat).Mul(inputRate, new(big.Rat).SetInt64(inputTokens))
	outputCost := new(big.Rat).Mul(outputRate, new(big.Rat).SetInt64(outputTokens))
	total := new(big.Rat).Add(inputCost, outputCost)
	return total.FloatString(10), nil
}
