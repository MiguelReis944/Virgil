package controlauth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLoadOrCreateCreatesAndReloadsCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "control.token")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.Bearer() == "" || first.Bearer() != second.Bearer() {
		t.Fatal("credential was empty or changed after reload")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first.Bearer())
	if err != nil {
		t.Fatalf("credential is not raw URL base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded credential length = %d, want 32", len(decoded))
	}
	if !first.Verify(first.Bearer()) || first.Verify("") || first.Verify("wrong") {
		t.Fatal("credential verification did not enforce exact secret")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows reports synthesized 0666 mode bits even when the file's ACL is
	// private, so Unix mode bits cannot prove ACL ownership there.
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got&0o077 != 0 {
		t.Fatalf("credential permissions = %o, want no group/other bits", got)
	}
}

func TestLoadOrCreateRejectsInvalidCredentialFiles(t *testing.T) {
	for name, content := range map[string]string{
		"empty":   "",
		"garbage": "not base64!",
		"short":   base64.RawURLEncoding.EncodeToString(make([]byte, 31)),
		"long":    base64.RawURLEncoding.EncodeToString(make([]byte, 33)),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.token")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreate(path); err == nil {
				t.Fatal("LoadOrCreate accepted invalid credential")
			}
		})
	}
}

func TestLoadOrCreateConcurrentCallersShareOneCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.token")
	const callers = 16
	values := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := LoadOrCreate(path)
			if err != nil {
				errs <- err
				return
			}
			values <- credential.Bearer()
		}()
	}
	wg.Wait()
	close(values)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var want string
	for value := range values {
		if want == "" {
			want = value
		}
		if value != want {
			t.Fatal("concurrent callers observed different credentials")
		}
	}
}
