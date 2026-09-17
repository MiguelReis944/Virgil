package telemetry

import "testing"

func TestUsageFromResult(t *testing.T) {
	in := int64(10)
	out := int64(5)
	cached := int64(3)
	u := FromResult("provider", &in, &out, &cached)
	if u.Source != "provider" || *u.InputTokens != 10 || *u.OutputTokens != 5 || *u.CachedTokens != 3 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestUnknownUsageHasNilCounts(t *testing.T) {
	u := FromResult("unknown", nil, nil, nil)
	if u.InputTokens != nil || u.OutputTokens != nil || u.CachedTokens != nil {
		t.Fatalf("unexpected counts in unknown usage: %+v", u)
	}
}
