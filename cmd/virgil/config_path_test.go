package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPathFromInstallationIndependentOfProject(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	installation := t.TempDir()
	project := t.TempDir()
	configPath := filepath.Join(installation, "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[storage]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(installation, "virgil.exe")
	for _, cwd := range []string{installation, project} {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		got, err := configPathFrom("", executable)
		if err != nil || got != configPath {
			t.Fatalf("cwd=%s path=%q err=%v", cwd, got, err)
		}
	}
}

func TestConfigPathFromExplicitHome(t *testing.T) {
	home := t.TempDir()
	got, err := configPathFrom(home, "")
	if err != nil || got != filepath.Join(home, "virgil.toml") {
		t.Fatalf("path=%q err=%v", got, err)
	}
	if _, err := configPathFrom("relative", ""); err == nil {
		t.Fatal("relative VIRGIL_HOME accepted")
	}
}
