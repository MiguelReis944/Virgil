package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	if response.StatusCode != http.StatusOK {
		t.Fatalf("panel status = %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("core did not stop after cancellation")
	}
}
