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

func TestLoadDoesNotResolveMissingSecretToLiteral(t *testing.T) {
	path := writeConfig(t, "[providers.openai]\napi_key = \"\u0024{OPENAI_API_KEY}\"\n")
	_, err := Load(path, func(string) string { return "" })
	if err == nil {
		t.Fatal("accepted missing provider key environment variable")
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
