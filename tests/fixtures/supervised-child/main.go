// supervised-child is a local-only executable used by the circuit-break tests.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	marker := os.Getenv("VIRGIL_TEST_MARKER")
	if marker == "" {
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "descendant" {
		for {
			_ = os.WriteFile(filepath.Join(marker, "heartbeat"), []byte(fmt.Sprint(time.Now().UnixNano())), 0o600)
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err := os.MkdirAll(marker, 0o700); err != nil {
		panic(err)
	}
	if os.Getenv("VIRGIL_RUN_ID") == "" || os.Getenv("VIRGIL_GATEWAY_URL") == "" || os.Getenv("VIRGIL_RUN_TOKEN") == "" || os.Getenv("OPENAI_API_KEY") != os.Getenv("VIRGIL_RUN_TOKEN") || os.Getenv("VIRGIL_PROVIDER_KEY_E2E") != "" {
		_ = os.WriteFile(filepath.Join(marker, "environment.invalid"), []byte("invalid supervised environment"), 0o600)
		os.Exit(3)
	}
	if expected := os.Getenv("VIRGIL_TEST_EXPECT_RUN_ID"); expected != "" && expected != os.Getenv("VIRGIL_RUN_ID") {
		_ = os.WriteFile(filepath.Join(marker, "environment.invalid"), []byte("wrong run identity"), 0o600)
		os.Exit(3)
	}
	_ = os.WriteFile(filepath.Join(marker, "environment.valid"), nil, 0o600)
	self, err := os.Executable()
	if err != nil {
		panic(err)
	}
	child := exec.Command(self, "descendant")
	child.Env = []string{"VIRGIL_TEST_MARKER=" + marker}
	if err := child.Start(); err != nil {
		panic(err)
	}
	_ = os.WriteFile(filepath.Join(marker, "root.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
	_ = os.WriteFile(filepath.Join(marker, "descendant.pid"), []byte(fmt.Sprint(child.Process.Pid)), 0o600)
	if len(os.Args) > 1 && os.Args[1] == "idle" {
		for {
			time.Sleep(time.Second)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for i := 1; i <= 2; i++ {
		body := []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"prompt-canary-supervised"}],"max_tokens":16}`)
		req, err := http.NewRequest(http.MethodPost, os.Getenv("VIRGIL_GATEWAY_URL")+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			panic(err)
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv("VIRGIL_RUN_TOKEN"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		_ = os.WriteFile(filepath.Join(marker, fmt.Sprintf("request-%d.status", i)), []byte(fmt.Sprint(resp.StatusCode)), 0o600)
	}
	for {
		time.Sleep(time.Second)
	}
}
