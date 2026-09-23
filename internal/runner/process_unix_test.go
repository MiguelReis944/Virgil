//go:build !windows

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessTreeHelper(t *testing.T) {
	role := os.Getenv("VIRGIL_TEST_PROCESS_ROLE")
	if role == "" {
		return
	}
	marker := os.Getenv("VIRGIL_TEST_PROCESS_MARKER")
	if err := os.WriteFile(filepath.Join(marker, role+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if role == "root" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		child := exec.Command(os.Args[0], "-test.run=^TestUnixProcessTreeHelper$")
		child.Env = append(os.Environ(), "VIRGIL_TEST_PROCESS_ROLE=child")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		<-signals
		_ = child.Wait()
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	<-signals
}

func TestUnixTerminateReapsProcessTree(t *testing.T) {
	dir := t.TempDir()
	proc, err := Start(context.Background(), RunSpec{
		Command: []string{os.Args[0], "-test.run=^TestUnixProcessTreeHelper$"},
		Env: map[string]string{
			"VIRGIL_TEST_PROCESS_ROLE":   "root",
			"VIRGIL_TEST_PROCESS_MARKER": dir,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Terminate(context.Background())
	root := waitForUnixPID(t, filepath.Join(dir, "root.pid"))
	child := waitForUnixPID(t, filepath.Join(dir, "child.pid"))
	if proc.PID() != root {
		t.Fatalf("PID() = %d, want root %d", proc.PID(), root)
	}
	if err := proc.Terminate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again := proc.Wait(); again != proc.Wait() {
		t.Fatalf("Wait returned different results: %+v, %+v", again, proc.Wait())
	}
	for _, pid := range []int{root, child} {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("pid %d remains after Terminate: %v", pid, err)
		}
	}
}

func waitForUnixPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process marker %s never appeared", path)
	return 0
}
