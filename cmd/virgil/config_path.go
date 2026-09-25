package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultConfigPath keeps the Virgil installation independent from the
// working directory of the project opened by Codex or Claude Code.
func defaultConfigPath() (string, error) {
	executable, _ := os.Executable()
	return configPathFrom(os.Getenv("VIRGIL_HOME"), executable)
}

func configPathFrom(home, executable string) (string, error) {
	if home != "" {
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("VIRGIL_HOME must be an absolute path")
		}
		return filepath.Join(home, "virgil.toml"), nil
	}
	if executable != "" {
		candidate := filepath.Join(filepath.Dir(executable), "virgil.toml")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "virgil.toml", nil
}
