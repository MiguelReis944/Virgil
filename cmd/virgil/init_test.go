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

func TestInitUsesVirgilHomeOutsideProject(t *testing.T) {
	home := filepath.Join(t.TempDir(), "installation")
	project := t.TempDir()
	t.Setenv("VIRGIL_HOME", home)
	t.Chdir(project)
	if err := runInit(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "virgil.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "virgil.toml")); !os.IsNotExist(err) {
		t.Fatalf("project config polluted: %v", err)
	}
}
