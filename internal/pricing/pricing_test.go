package pricing

import (
	"strings"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

var testTable = Table{
	Version:        "2024-01",
	Currency:       "USD",
	InputPerToken:  "0.000003",
	OutputPerToken: "0.000015",
	CachedPerToken: "0.0000015",
}

func ptr[T any](v T) *T { return &v }

func TestMeasuredUsageActualCost(t *testing.T) {
	u := telemetry.Usage{Source: "provider", InputTokens: ptr(int64(1000)), OutputTokens: ptr(int64(500))}
	cost, err := Calculate(u, testTable)
	if err != nil {
		t.Fatal(err)
	}
	if cost.ActualCost == nil || cost.EstimatedCost != nil {
		t.Fatalf("expected actual cost only: %+v", cost)
	}
	// 1000*0.000003 + 500*0.000015 = 0.003 + 0.0075 = 0.0105
	if !strings.HasPrefix(*cost.ActualCost, "0.0105") {
		t.Fatalf("wrong cost: %s", *cost.ActualCost)
	}
}

func TestEstimatedUsageEstimatedCost(t *testing.T) {
	u := telemetry.Usage{Source: "estimated", InputTokens: ptr(int64(100)), OutputTokens: ptr(int64(50))}
	cost, err := Calculate(u, testTable)
	if err != nil {
		t.Fatal(err)
	}
	if cost.EstimatedCost == nil || cost.ActualCost != nil {
		t.Fatalf("expected estimated cost only: %+v", cost)
	}
}

func TestUnknownUsageHasNullCost(t *testing.T) {
	u := telemetry.Usage{Source: "unknown"}
	cost, err := Calculate(u, testTable)
	if err != nil || cost.ActualCost != nil || cost.EstimatedCost != nil {
		t.Fatalf("expected nil cost: %+v err=%v", cost, err)
	}
}

func TestCachedTokensNotDoubleCounted(t *testing.T) {
	// 300 input, 100 cached → 200 normal + 100 cached, 200 output
	u := telemetry.Usage{
		Source:       "provider",
		InputTokens:  ptr(int64(300)),
		OutputTokens: ptr(int64(200)),
		CachedTokens: ptr(int64(100)),
	}
	cost, err := Calculate(u, testTable)
	if err != nil {
		t.Fatal(err)
	}
	// 200*0.000003 + 100*0.0000015 + 200*0.000015 = 0.0006 + 0.00015 + 0.003 = 0.00375
	if cost.ActualCost == nil || !strings.HasPrefix(*cost.ActualCost, "0.0037") {
		t.Fatalf("cached tokens doubled or wrong cost: %v", cost.ActualCost)
	}
	// Verify not 300*0.000003 + 100*0.0000015 + 200*0.000015 (that would be 0.004050)
	if strings.HasPrefix(*cost.ActualCost, "0.004") {
		t.Fatalf("cached tokens were double-counted: %s", *cost.ActualCost)
	}
}

func TestDecimalPrecision(t *testing.T) {
	table := Table{
		Currency:       "USD",
		InputPerToken:  "0.000001",
		OutputPerToken: "0.000001",
	}
	u := telemetry.Usage{Source: "provider", InputTokens: ptr(int64(1)), OutputTokens: ptr(int64(1))}
	cost, err := Calculate(u, table)
	if err != nil || cost.ActualCost == nil {
		t.Fatalf("err=%v cost=%+v", err, cost)
	}
	// 2 * 0.000001 = 0.000002 — must not lose precision
	if !strings.Contains(*cost.ActualCost, "0.000002") {
		t.Fatalf("precision lost: %s", *cost.ActualCost)
	}
}

func TestEmptyTableReturnsNilCost(t *testing.T) {
	u := telemetry.Usage{Source: "provider", InputTokens: ptr(int64(100)), OutputTokens: ptr(int64(50))}
	cost, err := Calculate(u, Table{})
	if err != nil || cost.ActualCost != nil || cost.EstimatedCost != nil {
		t.Fatalf("expected nil cost for empty table: %+v err=%v", cost, err)
	}
}
