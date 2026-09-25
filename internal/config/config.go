package config

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"

	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server       ServerConfig              `toml:"server"`
	Storage      StorageConfig             `toml:"storage"`
	ControlPlane ControlPlaneConfig        `toml:"control_plane"`
	Privacy      PrivacyConfig             `toml:"privacy"`
	Guardrails   GuardrailsConfig          `toml:"guardrails"`
	Providers    map[string]ProviderConfig `toml:"providers"`
	Pricing      PricingConfig             `toml:"pricing"`
	Dashboard    DashboardConfig           `toml:"dashboard"`
}

type DashboardConfig struct {
	Password string `toml:"password"` // plain text or ${ENV_VAR}
}

type PricingConfig struct {
	Version  string                     `toml:"version"`
	Currency string                     `toml:"currency"` // ISO 4217
	Models   map[string]ModelPriceEntry `toml:"models"`
}

type ModelPriceEntry struct {
	InputPerToken  string `toml:"input_per_token"`
	OutputPerToken string `toml:"output_per_token"`
	CachedPerToken string `toml:"cached_per_token"`
}

type GuardrailsConfig struct {
	MaxRequestsPerRun            int64    `toml:"max_requests_per_run"`
	MaxCostPerRunUSD             string   `toml:"max_cost_per_run_usd"`
	MaxInputTokensPerRun         int64    `toml:"max_input_tokens_per_run"`
	MaxOutputTokensPerRun        int64    `toml:"max_output_tokens_per_run"`
	MaxTotalTokensPerRun         int64    `toml:"max_total_tokens_per_run"`
	MaxDurationSeconds           int64    `toml:"max_duration_seconds"`
	MaxToolCallsPerRun           int64    `toml:"max_tool_calls_per_run"`
	AllowedProviders             []string `toml:"allowed_providers"`
	AllowedModels                []string `toml:"allowed_models"`
	AllowedTools                 []string `toml:"allowed_tools"`
	EstimatedCostPerCallUSD      string   `toml:"estimated_cost_per_call_usd"`
	EstimatedInputTokensPerCall  int64    `toml:"estimated_input_tokens_per_call"`
	EstimatedOutputTokensPerCall int64    `toml:"estimated_output_tokens_per_call"`
	// Installation-wide daily limits — independent of run_id, cannot be bypassed.
	MaxCallsPerDay   int64  `toml:"max_calls_per_day"`
	MaxCostPerDayUSD string `toml:"max_cost_per_day_usd"`
}

type ServerConfig struct {
	Listen   string `toml:"listen"`
	LogLevel string `toml:"log_level"`
}

type StorageConfig struct {
	Path          string `toml:"path"`
	RetentionDays int    `toml:"retention_days"`
}

type ControlPlaneConfig struct {
	Enabled        bool     `toml:"enabled"`
	Endpoint       string   `toml:"endpoint"`
	CredentialPath string   `toml:"credential_path"` // local file holding the scoped credential; gitignored
	AllowedFields  []string `toml:"allowed_fields"`
}

type PrivacyConfig struct {
	CapturePrompts       bool `toml:"capture_prompts"`
	CaptureResponses     bool `toml:"capture_responses"`
	CaptureToolArguments bool `toml:"capture_tool_arguments"`
}

type ProviderConfig struct {
	Type         string   `toml:"type"`
	BaseURL      string   `toml:"base_url"`
	Model        string   `toml:"model"`
	APIKey       string   `toml:"api_key"`
	APIKeyEnv    string   `toml:"-"`
	Capabilities []string `toml:"capabilities"`
	// Local marks the provider as explicitly local (e.g. Ollama on 127.0.0.1).
	// When true, SSRF validation is skipped. Only loopback base_url values are permitted.
	Local bool `toml:"local"`
}

var envReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
var policyDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)
var policyLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)

func Load(path string, _ func(string) string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Config{
		Server:  ServerConfig{Listen: "127.0.0.1:8787", LogLevel: "info"},
		Storage: StorageConfig{Path: "./data/virgil.db", RetentionDays: 30},
	}
	if err := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields().Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Privacy.CaptureToolArguments {
		return Config{}, fmt.Errorf("tool argument capture is unsupported")
	}
	switch cfg.Server.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("unsupported log level")
	}
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return Config{}, fmt.Errorf("invalid server listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return Config{}, fmt.Errorf("server listen address must be loopback")
	}
	if cfg.Storage.Path == "" {
		return Config{}, fmt.Errorf("storage path is required")
	}
	configDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve config directory: %w", err)
	}
	if !filepath.IsAbs(cfg.Storage.Path) {
		cfg.Storage.Path = filepath.Join(configDir, cfg.Storage.Path)
	}
	if cfg.ControlPlane.CredentialPath != "" && !filepath.IsAbs(cfg.ControlPlane.CredentialPath) {
		cfg.ControlPlane.CredentialPath = filepath.Join(configDir, cfg.ControlPlane.CredentialPath)
	}
	if cfg.Storage.RetentionDays < 0 {
		return Config{}, fmt.Errorf("retention_days cannot be negative")
	}
	if cfg.ControlPlane.Enabled {
		if cfg.ControlPlane.CredentialPath == "" || redaction.ValidateFields(cfg.ControlPlane.AllowedFields) != nil {
			return Config{}, fmt.Errorf("control plane requires credential_path and safe allowed_fields")
		}
		u, err := url.Parse(cfg.ControlPlane.Endpoint)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return Config{}, fmt.Errorf("invalid control plane endpoint")
		}
		if u.Scheme == "http" {
			ip := net.ParseIP(u.Hostname())
			if ip == nil || !ip.IsLoopback() {
				return Config{}, fmt.Errorf("control plane HTTP endpoint must be loopback")
			}
		}
	}
	g := cfg.Guardrails
	for _, n := range []int64{g.MaxRequestsPerRun, g.MaxInputTokensPerRun, g.MaxOutputTokensPerRun, g.MaxTotalTokensPerRun, g.MaxDurationSeconds, g.MaxToolCallsPerRun, g.EstimatedInputTokensPerCall, g.EstimatedOutputTokensPerCall, g.MaxCallsPerDay} {
		if n < 0 {
			return Config{}, fmt.Errorf("guardrail counts cannot be negative")
		}
	}
	for _, amount := range []string{g.MaxCostPerRunUSD, g.EstimatedCostPerCallUSD, g.MaxCostPerDayUSD} {
		if amount != "" && !policyDecimal.MatchString(amount) {
			return Config{}, fmt.Errorf("invalid guardrail cost")
		}
	}
	for _, labels := range [][]string{g.AllowedProviders, g.AllowedModels, g.AllowedTools} {
		for _, label := range labels {
			if !policyLabel.MatchString(label) {
				return Config{}, fmt.Errorf("invalid guardrail allowlist entry")
			}
		}
	}
	if cfg.Pricing.Currency != "" {
		var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
		if !currencyPattern.MatchString(cfg.Pricing.Currency) {
			return Config{}, fmt.Errorf("invalid pricing currency")
		}
	}
	var decimalPrice = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	for model, entry := range cfg.Pricing.Models {
		_ = model
		for _, p := range []string{entry.InputPerToken, entry.OutputPerToken} {
			if p != "" && !decimalPrice.MatchString(p) {
				return Config{}, fmt.Errorf("invalid pricing decimal")
			}
		}
		if entry.CachedPerToken != "" && !decimalPrice.MatchString(entry.CachedPerToken) {
			return Config{}, fmt.Errorf("invalid pricing decimal")
		}
	}
	if p := cfg.Dashboard.Password; p != "" {
		if match := envReference.FindStringSubmatch(p); match != nil {
			cfg.Dashboard.Password = os.Getenv(match[1])
		}
	}
	for name, provider := range cfg.Providers {
		seenCapabilities := make(map[string]bool)
		for _, capability := range provider.Capabilities {
			if (capability != "stream" && capability != "tools") || seenCapabilities[capability] {
				return Config{}, fmt.Errorf("provider %s has invalid capability", name)
			}
			seenCapabilities[capability] = true
		}
		if provider.APIKey != "" {
			match := envReference.FindStringSubmatch(provider.APIKey)
			if match == nil {
				return Config{}, fmt.Errorf("provider %s api_key must reference an environment variable", name)
			}
			provider.APIKeyEnv = match[1]
			provider.APIKey = ""
			cfg.Providers[name] = provider
		}
	}
	return cfg, nil
}
