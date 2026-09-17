package controlplane

// PolicyEnvelope wraps the remote policy with version metadata.
// Version is monotonic; callers must ignore envelopes with Version ≤ the last seen.
type PolicyEnvelope struct {
	Version int64    `json:"version"`
	ETag    string   `json:"etag,omitempty"`
	Limits  CPLimits `json:"limits"`
}

// CPLimits are the only fields a remote policy is permitted to set.
// Endpoint, export, and content-capture configuration changes are local-only.
type CPLimits struct {
	MaxCallsPerRun        int64    `json:"max_calls_per_run,omitempty"`
	MaxCostPerRunUSD      string   `json:"max_cost_per_run_usd,omitempty"`
	MaxInputTokensPerRun  int64    `json:"max_input_tokens_per_run,omitempty"`
	MaxOutputTokensPerRun int64    `json:"max_output_tokens_per_run,omitempty"`
	MaxTotalTokensPerRun  int64    `json:"max_total_tokens_per_run,omitempty"`
	MaxDurationSeconds    int64    `json:"max_duration_seconds,omitempty"`
	MaxToolCallsPerRun    int64    `json:"max_tool_calls_per_run,omitempty"`
	AllowedProviders      []string `json:"allowed_providers,omitempty"`
	AllowedModels         []string `json:"allowed_models,omitempty"`
	AllowedTools          []string `json:"allowed_tools,omitempty"`
}
