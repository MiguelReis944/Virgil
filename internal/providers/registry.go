package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/MiguelReis944/Virgil/internal/config"
)

type Registration struct {
	Adapter      Adapter
	Provider     string
	KeyEnv       string
	capabilities []string
}

// Registry maps routing keys to registrations.
// Keys are either "model" (when the model name is unambiguous) or "provider:model"
// (always registered; plain key removed if another provider uses the same model name).
type Registry map[string]Registration

func NewRegistry(cfg config.Config, client *http.Client) (Registry, error) {
	registry := make(Registry, len(cfg.Providers))
	// Track how many providers expose each plain model name.
	modelCount := make(map[string]int, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		modelCount[provider.Model]++
	}
	for name, provider := range cfg.Providers {
		if strings.Contains(name, ":") {
			return nil, fmt.Errorf("provider name must not contain ':' (got %q)", name)
		}
		for _, capability := range provider.Capabilities {
			if capability != "stream" && capability != "tools" {
				return nil, errors.New("unsupported provider capability")
			}
		}
		if provider.Model == "" || provider.BaseURL == "" {
			return nil, errors.New("provider model and base_url are required")
		}
		base, err := url.Parse(provider.BaseURL)
		if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") {
			return nil, errors.New("invalid provider base URL")
		}
		if !provider.Local {
			if err := validateBaseURL(provider.BaseURL); err != nil {
				return nil, fmt.Errorf("provider %s: %w", name, err)
			}
		}
		var adapter Adapter
		switch provider.Type {
		case "openai", "kimi", "openai-compatible":
			adapter = NewOpenAICompatible(provider.BaseURL, client)
		case "anthropic":
			adapter = NewAnthropic(provider.BaseURL, client)
		default:
			return nil, errors.New("unsupported provider type")
		}
		reg := Registration{
			Adapter: adapter, Provider: name, KeyEnv: provider.APIKeyEnv,
			capabilities: provider.Capabilities,
		}
		// Always register composite key for unambiguous selection.
		registry[name+":"+provider.Model] = reg
		// Register plain model key only when no other provider uses the same model name.
		if modelCount[provider.Model] == 1 {
			registry[provider.Model] = reg
		}
	}
	return registry, nil
}

func (r Registration) Validate(body json.RawMessage) error {
	if err := r.Adapter.Validate(body); err != nil {
		return err
	}
	if r.capabilities == nil {
		return nil
	}
	var request struct {
		Stream     bool            `json:"stream"`
		Tools      json.RawMessage `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
		Messages   []struct {
			Role      string          `json:"role"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return err
	}
	if request.Stream && !slices.Contains(r.capabilities, "stream") {
		return errors.New("provider streaming capability disabled")
	}
	usesTools := len(request.Tools) > 0 || len(request.ToolChoice) > 0
	for _, message := range request.Messages {
		usesTools = usesTools || message.Role == "tool" || len(message.ToolCalls) > 0
	}
	if usesTools && !slices.Contains(r.capabilities, "tools") {
		return errors.New("provider tool capability disabled")
	}
	return nil
}
