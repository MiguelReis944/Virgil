package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var credentialName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// HasCredential reports only whether a credential is available; it never returns its value.
func (s *Store) HasCredential(name string) bool {
	if !credentialName.MatchString(name) {
		return false
	}
	if os.Getenv(name) != "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.envPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && strings.TrimSpace(key) == name && strings.Trim(strings.TrimSpace(value), `"'`) != "" {
			return true
		}
	}
	return false
}

// SaveCredential keeps the value in the local environment file, never in TOML.
// The running gateway reads new credentials after its next restart.
func (s *Store) SaveCredential(ctx context.Context, name, value string) error {
	if !credentialName.MatchString(name) {
		return errors.New("invalid credential name")
	}
	if value == "" || len(value) > 16<<10 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") || strings.HasPrefix(value, `"`) || strings.HasSuffix(value, `"`) || strings.HasPrefix(value, "'") || strings.HasSuffix(value, "'") {
		return errors.New("invalid credential value")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := os.ReadFile(s.envPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read credential file: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	updated := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		key, _, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == name {
			if !replaced {
				updated = append(updated, name+"="+value)
				replaced = true
			}
			continue
		}
		updated = append(updated, line)
	}
	if !replaced {
		updated = append(updated, name+"="+value)
	}
	if err := os.MkdirAll(filepath.Dir(s.envPath), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.envPath), ".env.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect credential file: %w", err)
	}
	if _, err := tmp.WriteString(strings.Join(updated, "\n") + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync credential file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credential file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.replace(tmp.Name(), s.envPath); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	return nil
}
