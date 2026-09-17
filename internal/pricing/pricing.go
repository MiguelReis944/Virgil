// Package pricing computes estimated billing costs from token usage.
// Results are labeled as estimates of billing, not provider invoices.
package pricing

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// Table holds per-token prices for one model.
// Prices are decimal strings (e.g. "0.000003") denominated in Currency per token.
type Table struct {
	Version          string
	Currency         string // ISO 4217, e.g. "USD"
	InputPerToken    string // price per input token
	OutputPerToken   string // price per output token
	CachedPerToken   string // price per cached input token (replaces InputPerToken for cached portion)
}

// Cost holds the computed cost strings. At most one field is set;
// both are nil when usage or price is unknown.
type Cost struct {
	ActualCost    *string // set only for UsageSource=="provider"
	EstimatedCost *string // set only for UsageSource=="estimated"
}

func parseDecimal(s string) (*big.Rat, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, fmt.Errorf("invalid decimal price: %q", s)
	}
	return r, nil
}

// Calculate computes a billing-estimate cost from usage and a price table.
// Returns zero-value Cost (both nil) when usage is unknown or price table is empty.
// ponytail: big.Rat arithmetic; upgrade to a decimal lib if sub-cent rounding matters
func Calculate(usage telemetry.Usage, table Table) (Cost, error) {
	if usage.Source == "unknown" {
		return Cost{}, nil
	}
	if table.InputPerToken == "" || table.OutputPerToken == "" {
		return Cost{}, nil
	}
	if usage.InputTokens == nil && usage.OutputTokens == nil {
		return Cost{}, nil
	}

	inPrice, err := parseDecimal(table.InputPerToken)
	if err != nil {
		return Cost{}, err
	}
	outPrice, err := parseDecimal(table.OutputPerToken)
	if err != nil {
		return Cost{}, err
	}
	var cachedPrice *big.Rat
	if table.CachedPerToken != "" {
		p, err := parseDecimal(table.CachedPerToken)
		if err != nil {
			return Cost{}, err
		}
		cachedPrice = p
	}

	total := new(big.Rat)

	// Input tokens: cached portion uses cachedPerToken if configured.
	if usage.InputTokens != nil {
		nonCached := *usage.InputTokens
		if usage.CachedTokens != nil && cachedPrice != nil {
			cached := *usage.CachedTokens
			if cached < 0 || cached > nonCached {
				return Cost{}, errors.New("invalid cached token count")
			}
			cachedCost := new(big.Rat).Mul(new(big.Rat).SetInt64(cached), cachedPrice)
			total.Add(total, cachedCost)
			nonCached -= cached
		}
		inputCost := new(big.Rat).Mul(new(big.Rat).SetInt64(nonCached), inPrice)
		total.Add(total, inputCost)
	}

	if usage.OutputTokens != nil {
		outputCost := new(big.Rat).Mul(new(big.Rat).SetInt64(*usage.OutputTokens), outPrice)
		total.Add(total, outputCost)
	}

	str := total.FloatString(10)
	switch usage.Source {
	case "provider":
		return Cost{ActualCost: &str}, nil
	case "estimated":
		return Cost{EstimatedCost: &str}, nil
	default:
		return Cost{}, nil
	}
}
