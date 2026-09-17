package providers

import (
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
)

func TestRegistryUsesConfiguredProviderTypes(t *testing.T) {
	registry, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
		"openai": {Type: "openai", BaseURL: "https://example.invalid/v1", Model: "openai-model"},
		"kimi":   {Type: "kimi", BaseURL: "https://example.invalid/v1", Model: "kimi-model", Capabilities: []string{"stream"}},
		"custom": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", Model: "custom-model"},
	}}, nil)
	if err != nil || len(registry) != 3 || registry["kimi-model"].Provider != "kimi" {
		t.Fatalf("registry=%v err=%v", registry, err)
	}
	if err := registry["kimi-model"].Validate([]byte(`{"model":"kimi-model","messages":[{"role":"user","content":"synthetic"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`)); err == nil {
		t.Fatal("disabled tool capability accepted")
	}
	if err := registry["kimi-model"].Validate([]byte(`{"model":"kimi-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryRejectsDuplicateModel(t *testing.T) {
	_, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
		"a": {Type: "openai", BaseURL: "https://example.invalid/v1", Model: "same"},
		"b": {Type: "kimi", BaseURL: "https://example.invalid/v1", Model: "same"},
	}}, nil)
	if err == nil {
		t.Fatal("duplicate model accepted")
	}
}
