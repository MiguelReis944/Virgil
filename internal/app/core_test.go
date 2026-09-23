package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCoreOptionsRejectsInvalidPanelURLs(t *testing.T) {
	for _, panelURL := range []string{
		"https://127.0.0.1:8787/dashboard",
		"http://example.com/dashboard",
		"http://127.0.0.1:8787/other",
		"http://127.0.0.1:8787/dashboard.evil",
		"http://user@127.0.0.1:8787/dashboard",
		"http://127.0.0.1:8787/dashboard?token=secret",
	} {
		t.Run(panelURL, func(t *testing.T) {
			if err := OpenBrowser(panelURL); err == nil {
				t.Fatal("expected unsafe panel URL to be rejected")
			}
		})
	}
}

func TestCoreOptionsAcceptsLoopbackPanelURLs(t *testing.T) {
	for _, panelURL := range []string{
		"http://127.0.0.1:8787/dashboard",
		"http://[::1]:8787/dashboard/executions/run_1",
	} {
		if err := validatePanelURL(panelURL); err != nil {
			t.Fatalf("%s: %v", panelURL, err)
		}
	}
}

func TestCoreOptionsRunCoreRejectsOccupiedListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	dataPath := filepath.ToSlash(filepath.Join(t.TempDir(), "virgil.db"))
	configText := fmt.Sprintf("[server]\nlisten = %q\n[storage]\npath = %q\n", listener.Addr().String(), dataPath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	err = RunCore(context.Background(), CoreOptions{ConfigPath: configPath, OpenPanel: false})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("error = %v, want listener failure", err)
	}
}

func TestRunCoreRejectsMalformedConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[server\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error = %v, want actionable parse error", err)
	}
}

func TestRunCoreRejectsUnreadableConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := t.TempDir() // A directory cannot be read as a TOML file.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("error = %v, want actionable read error", err)
	}
}

func TestRunCoreRejectsInvalidConfigInsteadOfStartingSetup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	if err := os.WriteFile(configPath, []byte("[server]\nlisten = \"0.0.0.0:8787\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := RunCore(ctx, CoreOptions{ConfigPath: configPath})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error = %v, want loopback validation error", err)
	}
}

func TestCoreOptionsServesPanelUntilCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	configPath := filepath.Join(t.TempDir(), "virgil.toml")
	dataPath := filepath.ToSlash(filepath.Join(t.TempDir(), "virgil.db"))
	configText := fmt.Sprintf("[server]\nlisten = %q\n[storage]\npath = %q\n", address, dataPath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCore(ctx, CoreOptions{ConfigPath: configPath}) }()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	ready := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + address + "/health")
		if err == nil {
			response.Body.Close()
			ready = response.StatusCode == http.StatusOK
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("local core did not become ready")
	}
	response, err := client.Get("http://" + address + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	client.CloseIdleConnections()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("panel status = %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("core did not stop after cancellation")
	}
}

func TestServePanelWaitsForHTTPHandlerBeforeOpening(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(url string) error {
			opened <- url
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness probe did not reach handler")
	}
	select {
	case <-opened:
		t.Fatal("browser opened before handler answered")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case got := <-opened:
		if got != panelURL {
			t.Fatalf("opened %q, want %q", got, panelURL)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("browser did not open after handler answered")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServePanelDoesNotOpenAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	started := make(chan struct{})
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(string) error {
			opened <- struct{}{}
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness probe did not reach handler")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	select {
	case <-opened:
		t.Fatal("browser opened after cancellation")
	default:
	}
}

func TestServePanelDoesNotOpenMissingPanelPage(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	panelURL := "http://" + listener.Addr().String() + "/dashboard"
	served := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		select {
		case served <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opened := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- servePanel(ctx, listener, handler, panelURL, func(string) error {
			opened <- struct{}{}
			return nil
		})
	}()
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("panel probe did not reach handler")
	}
	select {
	case <-opened:
		t.Fatal("browser opened a missing panel page")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}
