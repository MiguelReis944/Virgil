package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsAndLocalMode(t *testing.T) {
	path := writeConfig(t, "[storage]\npath = \"./data/virgil.db\"\n")
	cfg, err := Load(path, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Storage.Path != "./data/virgil.db" {
		t.Fatalf("storage path = %q", cfg.Storage.Path)
	}
	if cfg.ControlPlane.Enabled {
		t.Fatal("Control Plane enabled by default")
	}
}

func TestLoadRejectsNonLoopback(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:8787", "192.0.2.1:8787", "[::]:8787"} {
		t.Run(listen, func(t *testing.T) {
			path := writeConfig(t, "[server]\nlisten = \""+listen+"\"\n")
			if _, err := Load(path, os.Getenv); err == nil {
				t.Fatal("accepted non-loopback listener")
			}
		})
	}
}

func TestLoadRejectsMalformedAndMissingConfig(t *testing.T) {
	path := writeConfig(t, "[server\n")
	if _, err := Load(path, os.Getenv); err == nil {
		t.Fatal("accepted malformed TOML")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml"), os.Getenv); err == nil {
		t.Fatal("accepted missing config")
	}
}

func TestLoadKeepsProviderKeyReferenceLocal(t *testing.T) {
	path := writeConfig(t, "[providers.openai]\napi_key = \"\u0024{OPENAI_API_KEY}\"\n")
	cfg, err := Load(path, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["openai"].APIKey != "" || cfg.Providers["openai"].APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("provider key reference not preserved safely: %+v", cfg.Providers["openai"])
	}
}

func TestLoadRejectsContentCaptureAndUnknownOptions(t *testing.T) {
	for _, body := range []string{
		"[privacy]\ncapture_prompts = true\n",
		"[privacy]\ncapture_responses = true\n",
		"[privacy]\ncapture_tool_arguments = true\n",
		"[privacy]\nunknown_option = true\n",
	} {
		path := writeConfig(t, body)
		if _, err := Load(path, os.Getenv); err == nil {
			t.Fatalf("accepted unsafe or unknown config: %q", body)
		}
	}
}

func TestLoadRejectsUnknownLogLevel(t *testing.T) {
	path := writeConfig(t, "[server]\nlog_level = \"trace\"\n")
	if _, err := Load(path, os.Getenv); err == nil {
		t.Fatal("accepted unsupported log level")
	}
}

func TestLoadGuardrails(t *testing.T) {
	path := writeConfig(t, `[guardrails]
max_requests_per_run = 2
max_cost_per_run_usd = "1.25"
max_input_tokens_per_run = 100
max_output_tokens_per_run = 50
max_total_tokens_per_run = 150
max_duration_seconds = 60
max_tool_calls_per_run = 3
allowed_providers = ["fixture"]
allowed_models = ["fixture-model"]
allowed_tools = ["fixture_tool"]
estimated_cost_per_call_usd = "0.10"
estimated_input_tokens_per_call = 10
estimated_output_tokens_per_call = 5
`)
	cfg, err := Load(path, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Guardrails.MaxRequestsPerRun != 2 || cfg.Guardrails.MaxCostPerRunUSD != "1.25" || len(cfg.Guardrails.AllowedTools) != 1 {
		t.Fatalf("guardrails not loaded: %+v", cfg.Guardrails)
	}
}

func TestLoadRejectsInvalidGuardrails(t *testing.T) {
	for _, body := range []string{
		"[guardrails]\nmax_requests_per_run = -1\n",
		"[guardrails]\nmax_cost_per_run_usd = \"bad\"\n",
		"[guardrails]\nmax_duration_seconds = -1\n",
		"[guardrails]\nallowed_models = [\"bad model\"]\n",
		"[guardrails]\nestimated_cost_per_call_usd = \"-0.1\"\n",
	} {
		if _, err := Load(writeConfig(t, body), os.Getenv); err == nil {
			t.Fatalf("accepted invalid guardrails: %q", body)
		}
	}
}
