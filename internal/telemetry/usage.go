package telemetry

// Usage holds token counts with explicit provenance.
// CachedTokens, when set, is a subset of InputTokens.
type Usage struct {
	Source       string // "provider", "estimated", or "unknown"
	InputTokens  *int64
	OutputTokens *int64
	CachedTokens *int64
}

// FromResult constructs a Usage from provider result fields.
func FromResult(source string, input, output, cached *int64) Usage {
	return Usage{Source: source, InputTokens: input, OutputTokens: output, CachedTokens: cached}
}
