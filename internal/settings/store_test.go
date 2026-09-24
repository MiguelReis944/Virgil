package settings

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
)

func TestStoreUpdateIsAtomicValidatedAndPreservesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	original := "[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\nretention_days=7\n[privacy]\ncapture_prompts=true\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	if err := store.Update(context.Background(), func(cfg *config.Config) error {
		cfg.Providers = map[string]config.ProviderConfig{"local": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen", Local: true, APIKeyEnv: "LOCAL_KEY"}}
		cfg.Guardrails.MaxRequestsPerRun = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Privacy.CapturePrompts || got.Storage.RetentionDays != 7 || got.Guardrails.MaxRequestsPerRun != 4 || got.Providers["local"].APIKeyEnv != "LOCAL_KEY" {
		t.Fatalf("config not preserved: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	matches, _ := filepath.Glob(path + ".tmp-*")
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestStoreUpdateLeavesInvalidFileByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	original := []byte("[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := New(path).Update(context.Background(), func(cfg *config.Config) error { cfg.Server.Listen = "0.0.0.0:8787"; return nil })
	if err == nil {
		t.Fatal("expected validation error")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatalf("file changed:\n%s", after)
	}
	matches, _ := filepath.Glob(path + ".tmp-*")
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestStoreUpdateSerializesConcurrentMutations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(path, []byte("[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(path)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Update(context.Background(), func(cfg *config.Config) error { cfg.Guardrails.MaxRequestsPerRun++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	cfg, err := config.Load(path, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Guardrails.MaxRequestsPerRun != 12 {
		t.Fatalf("updates=%d", cfg.Guardrails.MaxRequestsPerRun)
	}
}

func TestStoreUpdatePreservesEnvironmentReferencesWithoutResolvingSecrets(t *testing.T) {
	t.Setenv("DASH_SECRET", "must-not-be-written")
	path := filepath.Join(t.TempDir(), "virgil.toml")
	original := "[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n[dashboard]\npassword='${DASH_SECRET}'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(path).Update(context.Background(), func(cfg *config.Config) error { cfg.Guardrails.MaxRequestsPerRun = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "${DASH_SECRET}") || strings.Contains(string(raw), "must-not-be-written") {
		t.Fatalf("secret reference was not preserved:\n%s", raw)
	}
}

func TestStoreUpdateReplacesExistingDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	writeValidConfig(t, path)
	store := New(path)
	if err := store.Update(context.Background(), func(cfg *config.Config) error { cfg.Guardrails.MaxRequestsPerRun = 9; return nil }); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Guardrails.MaxRequestsPerRun != 9 {
		t.Fatalf("value=%d", cfg.Guardrails.MaxRequestsPerRun)
	}
}

func TestStoreUpdatePreservesOriginalWhenAtomicReplaceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	writeValidConfig(t, path)
	before, _ := os.ReadFile(path)
	store := New(path)
	store.replace = func(string, string) error { return os.ErrPermission }
	if err := store.Update(context.Background(), func(cfg *config.Config) error { cfg.Guardrails.MaxRequestsPerRun = 9; return nil }); err == nil {
		t.Fatal("expected replace error")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("original changed:\n%s", after)
	}
	matches, _ := filepath.Glob(path + ".tmp-*")
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestStoreUpdateRejectsInvalidProviderWithoutChangingOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	writeValidConfig(t, path)
	before, _ := os.ReadFile(path)
	err := New(path).Update(context.Background(), func(cfg *config.Config) error {
		cfg.Providers = map[string]config.ProviderConfig{"bad": {Type: "openai-compatible", BaseURL: "http://example.com/v1", Model: "m", Local: true}}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("invalid provider changed config")
	}
}

func writeValidConfig(t *testing.T, path string) {
	t.Helper()
	raw := "[server]\nlisten='127.0.0.1:8787'\nlog_level='info'\n[storage]\npath='./data/test.db'\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}
