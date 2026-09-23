//go:build !windows

package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type unixProcess struct{ pid int }

func startPlatform(cmd *exec.Cmd) (platformProcess, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return unixProcess{pid: cmd.Process.Pid}, nil
}

func (p unixProcess) terminate(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	if err := signalGroup(p.pid, syscall.SIGTERM); err != nil {
		return err
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}
	return signalGroup(p.pid, syscall.SIGKILL)
}

func (p unixProcess) close() error { return signalGroup(p.pid, syscall.SIGKILL) }

func signalGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("process %d: %w", pid, err)
}
