package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/providers"
)

func TestInitProducesRunnableProviderConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.toml")
	if err := runInit([]string{"--output", path}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := providers.NewRegistry(cfg, http.DefaultClient); err != nil {
		t.Fatalf("generated provider config is not runnable: %v", err)
	}
}
