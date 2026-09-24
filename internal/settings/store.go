// Package settings persists panel-initiated configuration changes.
package settings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/MiguelReis944/Virgil/internal/config"
	"github.com/MiguelReis944/Virgil/internal/providers"
	"github.com/pelletier/go-toml/v2"
)

type Store struct {
	path    string
	mu      sync.Mutex
	replace func(string, string) error
}

func New(path string) *Store { return &Store{path: path, replace: atomicReplace} }

func (s *Store) Path() string { return s.path }

func (s *Store) Load() (config.Config, error) { return config.Load(s.path, os.Getenv) }

func HashFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) Update(ctx context.Context, mutate func(*config.Config) error) error {
	if mutate == nil {
		return fmt.Errorf("settings mutation is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := config.Load(s.path, os.Getenv); err != nil {
		return err
	}
	rawCurrent, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg := config.Config{Server: config.ServerConfig{Listen: "127.0.0.1:8787", LogLevel: "info"}, Storage: config.StorageConfig{Path: "./data/virgil.db", RetentionDays: 30}}
	if err := toml.NewDecoder(bytes.NewReader(rawCurrent)).DisallowUnknownFields().Decode(&cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if err := mutate(&cfg); err != nil {
		return err
	}
	for name, provider := range cfg.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = "${" + provider.APIKeyEnv + "}"
		}
		cfg.Providers[name] = provider
	}
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	closeWithError := func(cause error) error { _ = tmp.Close(); return cause }
	if err := tmp.Chmod(0o600); err != nil {
		return closeWithError(fmt.Errorf("protect temporary config: %w", err))
	}
	if _, err := tmp.Write(raw); err != nil {
		return closeWithError(fmt.Errorf("write temporary config: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return closeWithError(fmt.Errorf("sync temporary config: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	validated, err := config.Load(tmpName, os.Getenv)
	if err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if err := providers.ValidateConfig(validated); err != nil {
		return fmt.Errorf("validate providers: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("protect config: %w", err)
	}
	if err := s.replace(tmpName, s.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
