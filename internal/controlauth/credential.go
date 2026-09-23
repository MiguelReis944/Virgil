// Package controlauth manages the installation-local credential used by the
// loopback control protocol.
package controlauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const credentialBytes = 32

var createMu sync.Mutex

// Credential retains the plaintext only in the process that must send it and
// a fixed-size hash for constant-time verification.
type Credential struct {
	value string
	hash  [sha256.Size]byte
}

// LoadOrCreate loads a valid credential or creates it exactly once with
// owner-only mode bits. The exclusive destination creation prevents one local
// starter from replacing a credential another starter already observed.
func LoadOrCreate(path string) (Credential, error) {
	createMu.Lock()
	defer createMu.Unlock()

	credential, err := load(path)
	if err == nil {
		return credential, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Credential{}, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Credential{}, fmt.Errorf("create control credential directory: %w", err)
	}
	raw := make([]byte, credentialBytes)
	if _, err := rand.Read(raw); err != nil {
		return Credential{}, fmt.Errorf("generate control credential: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return load(path)
	}
	if err != nil {
		return Credential{}, fmt.Errorf("create control credential: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(value); err != nil {
		return Credential{}, fmt.Errorf("write control credential: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Credential{}, fmt.Errorf("sync control credential: %w", err)
	}
	if err := file.Close(); err != nil {
		return Credential{}, fmt.Errorf("close control credential: %w", err)
	}
	ok = true
	return newCredential(value), nil
}

func load(path string) (Credential, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, err
	}
	value := string(encoded)
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != credentialBytes {
		return Credential{}, fmt.Errorf("invalid control credential file %q", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Credential{}, fmt.Errorf("secure control credential: %w", err)
	}
	return newCredential(value), nil
}

func newCredential(value string) Credential {
	return Credential{value: value, hash: sha256.Sum256([]byte(value))}
}

// Bearer returns the credential for the loopback Authorization header.
func (c Credential) Bearer() string { return c.value }

// Verify compares a candidate without data-dependent early exit.
func (c Credential) Verify(candidate string) bool {
	if candidate == "" || c.value == "" {
		return false
	}
	hash := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(hash[:], c.hash[:]) == 1
}
