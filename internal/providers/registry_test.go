package providers

import (
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
)

func TestRegistryUsesConfiguredProviderTypes(t *testing.T) {
	registry, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
		"openai": {Type: "openai", BaseURL: "https://example.invalid/v1", Model: "openai-model"},
		"kimi":   {Type: "kimi", BaseURL: "https://example.invalid/v1", Model: "kimi-model", Capabilities: []string{"stream"}},
		"custom": {Type: "openai-compatible", BaseURL: "https://example.invalid/v1", Model: "custom-model"},
	}}, nil)
	// 3 providers × 2 keys each (plain + composite) = 6 entries.
	if err != nil || len(registry) != 6 || registry["kimi-model"].Provider != "kimi" {
		t.Fatalf("registry=%v err=%v", registry, err)
	}
	// Composite key must also work.
	if registry["kimi:kimi-model"].Provider != "kimi" {
		t.Fatalf("composite key missing")
	}
	if err := registry["kimi-model"].Validate([]byte(`{"model":"kimi-model","messages":[{"role":"user","content":"synthetic"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`)); err == nil {
		t.Fatal("disabled tool capability accepted")
	}
	if err := registry["kimi-model"].Validate([]byte(`{"model":"kimi-model","messages":[{"role":"user","content":"synthetic"}],"stream":true}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryDuplicateModelUsesCompositeKey(t *testing.T) {
	registry, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
		"a": {Type: "openai", BaseURL: "https://example.invalid/v1", Model: "same"},
		"b": {Type: "kimi", BaseURL: "https://example.invalid/v1", Model: "same"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Plain "same" key must NOT exist (ambiguous); only composite keys.
	if _, ok := registry["same"]; ok {
		t.Fatal("ambiguous plain key should not be registered")
	}
	if _, ok := registry["a:same"]; !ok {
		t.Fatal("composite key a:same missing")
	}
	if _, ok := registry["b:same"]; !ok {
		t.Fatal("composite key b:same missing")
	}
}

func TestRegistryAllowsExplicitLocalOllama(t *testing.T) {
	registry, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
		"ollama": {Type: "ollama", BaseURL: "http://localhost:11434/v1", Model: "llama3", Local: true},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if registry["llama3"].Provider != "ollama" {
		t.Fatalf("ollama registration missing: %#v", registry)
	}
}

func TestRegistryValidatesResponsesBackend(t *testing.T) {
	for _, tc := range []struct {
		name, providerType, backend string
		wantErr                    bool
	}{
		{"native default", "openai-compatible", "", false},
		{"chat translation", "openai-compatible", "chat-completions", false},
		{"unknown backend", "openai-compatible", "something-else", true},
		{"anthropic cannot translate", "anthropic", "chat-completions", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(config.Config{Providers: map[string]config.ProviderConfig{
				"fixture": {Type: tc.providerType, BaseURL: "https://example.invalid/v1", Model: "fixture-model", ResponsesBackend: tc.backend},
			}}, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewRegistry() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
