package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server       ServerConfig              `toml:"server"`
	Storage      StorageConfig             `toml:"storage"`
	ControlPlane ControlPlaneConfig        `toml:"control_plane"`
	Privacy      PrivacyConfig             `toml:"privacy"`
	Providers    map[string]ProviderConfig `toml:"providers"`
}

type ServerConfig struct {
	Listen   string `toml:"listen"`
	LogLevel string `toml:"log_level"`
}

type StorageConfig struct {
	Path          string `toml:"path"`
	RetentionDays int    `toml:"retention_days"`
}

type ControlPlaneConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
}

type PrivacyConfig struct {
	CapturePrompts       bool `toml:"capture_prompts"`
	CaptureResponses     bool `toml:"capture_responses"`
	CaptureToolArguments bool `toml:"capture_tool_arguments"`
}

type ProviderConfig struct {
	Type      string `toml:"type"`
	BaseURL   string `toml:"base_url"`
	Model     string `toml:"model"`
	APIKey    string `toml:"api_key"`
	APIKeyEnv string `toml:"-"`
}

var envReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

func Load(path string, getenv func(string) string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg := Config{
		Server:  ServerConfig{Listen: "127.0.0.1:8787", LogLevel: "info"},
		Storage: StorageConfig{Path: "./data/virgil.db", RetentionDays: 30},
	}
	if err := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields().Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Privacy.CapturePrompts || cfg.Privacy.CaptureResponses || cfg.Privacy.CaptureToolArguments {
		return Config{}, fmt.Errorf("content capture is unsupported")
	}
	switch cfg.Server.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("unsupported log level")
	}
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return Config{}, fmt.Errorf("invalid server listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return Config{}, fmt.Errorf("server listen address must be loopback")
	}
	if cfg.Storage.Path == "" {
		return Config{}, fmt.Errorf("storage path is required")
	}
	if cfg.Storage.RetentionDays < 0 {
		return Config{}, fmt.Errorf("retention_days cannot be negative")
	}
	for name, provider := range cfg.Providers {
		if provider.APIKey != "" {
			match := envReference.FindStringSubmatch(provider.APIKey)
			if match == nil {
				return Config{}, fmt.Errorf("provider %s api_key must reference an environment variable", name)
			}
			if getenv(match[1]) == "" {
				return Config{}, fmt.Errorf("provider %s api_key environment variable is unset", name)
			}
			provider.APIKeyEnv = match[1]
			provider.APIKey = ""
			cfg.Providers[name] = provider
		}
	}
	return cfg, nil
}
