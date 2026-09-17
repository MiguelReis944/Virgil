package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/MiguelReis944/Virgil/internal/config"
)

type Registration struct {
	Adapter      Adapter
	Provider     string
	KeyEnv       string
	capabilities []string
}

type Registry map[string]Registration

func NewRegistry(cfg config.Config, client *http.Client) (Registry, error) {
	registry := make(Registry, len(cfg.Providers))
	for name, provider := range cfg.Providers {
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
		if _, exists := registry[provider.Model]; exists {
			return nil, errors.New("duplicate configured model")
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
		registry[provider.Model] = Registration{
			Adapter: adapter, Provider: name, KeyEnv: provider.APIKeyEnv,
			capabilities: provider.Capabilities,
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
