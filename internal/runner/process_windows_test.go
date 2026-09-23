//go:build windows

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessTreeHelper(t *testing.T) {
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
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsProcessTreeHelper$")
		child.Env = append(os.Environ(), "VIRGIL_TEST_PROCESS_ROLE=child")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if os.Getenv("VIRGIL_TEST_PROCESS_EARLY_EXIT") == "1" {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(filepath.Join(marker, "child.pid")); err == nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			os.Exit(2)
		}
		_ = child.Wait()
		return
	}
	for {
		time.Sleep(time.Minute)
	}
}

func TestWindowsTerminateReapsProcessTree(t *testing.T) {
	dir := t.TempDir()
	proc, err := Start(context.Background(), RunSpec{
		Command: []string{os.Args[0], "-test.run=^TestWindowsProcessTreeHelper$"},
		Env: map[string]string{
			"VIRGIL_TEST_PROCESS_ROLE":   "root",
			"VIRGIL_TEST_PROCESS_MARKER": dir,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Terminate(context.Background())
	root := waitForWindowsPID(t, filepath.Join(dir, "root.pid"))
	child := waitForWindowsPID(t, filepath.Join(dir, "child.pid"))
	if proc.PID() != root {
		t.Fatalf("PID() = %d, want root %d", proc.PID(), root)
	}
	if err := proc.Terminate(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := proc.Wait()
	if again := proc.Wait(); first != again {
		t.Fatalf("Wait returned different results: %+v, %+v", first, again)
	}
	if err := proc.Terminate(context.Background()); err != nil {
		t.Fatalf("repeated Terminate: %v", err)
	}
	for _, pid := range []int{root, child} {
		if processActive(pid) {
			t.Fatalf("pid %d remains active after Terminate", pid)
		}
	}
}

func TestWindowsRootExitClosesJobAndKillsDescendant(t *testing.T) {
	dir := t.TempDir()
	proc, err := Start(context.Background(), RunSpec{
		Command: []string{os.Args[0], "-test.run=^TestWindowsProcessTreeHelper$"},
		Env: map[string]string{
			"VIRGIL_TEST_PROCESS_ROLE":       "root",
			"VIRGIL_TEST_PROCESS_MARKER":     dir,
			"VIRGIL_TEST_PROCESS_EARLY_EXIT": "1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proc.Terminate(context.Background())
	child := waitForWindowsPID(t, filepath.Join(dir, "child.pid"))
	if result := proc.Wait(); result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("root exit: %+v", result)
	}
	if processActive(child) {
		t.Fatalf("descendant %d survived root exit", child)
	}
}

func waitForWindowsPID(t *testing.T, path string) int {
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

func processActive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259 // STILL_ACTIVE
}
