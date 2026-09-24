package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// ProcessResult is the root process exit status. Err is the error returned by Wait.
type ProcessResult struct {
	ExitCode int
	Err      error
}

// Process owns the root process and all descendants it creates.
type Process interface {
	PID() int
	Wait() ProcessResult
	Terminate(context.Context) error
}

type platformProcess interface {
	terminate(context.Context, <-chan struct{}) error
	close() error
}

type ownedProcess struct {
	cmd       *exec.Cmd
	platform  platformProcess
	done      chan struct{}
	result    ProcessResult
	termDone  chan struct{}
	termErr   error
	termOnce  sync.Once
	closeErr  error
	closeOnce sync.Once
}

// Start launches the child in an operating-system process group or job object.
// The caller owns the returned Process and must call Wait or Terminate.
func Start(ctx context.Context, spec RunSpec) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(spec.Command) == 0 {
		return nil, errors.New("runner: command is required")
	}
	runID := spec.RunID
	if runID == "" {
		id, err := telemetry.NewID(16)
		if err != nil {
			return nil, err
		}
		runID = "run_" + id
	}
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Env = childEnvironment(spec, runID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	platform, err := startPlatform(cmd)
	if err != nil {
		return nil, err
	}
	return newOwnedProcess(cmd, platform), nil
}

func newOwnedProcess(cmd *exec.Cmd, platform platformProcess) *ownedProcess {
	proc := &ownedProcess{cmd: cmd, platform: platform, done: make(chan struct{}), termDone: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		proc.closePlatform()
		proc.result = ProcessResult{ExitCode: cmd.ProcessState.ExitCode(), Err: errors.Join(err, proc.closeErr)}
		close(proc.done)
	}()
	return proc
}

func (p *ownedProcess) closePlatform() {
	p.closeOnce.Do(func() { p.closeErr = p.platform.close() })
}

func childEnvironment(spec RunSpec, runID string) []string {
	raw := os.Environ()
	env := make([]string, 0, len(raw)+len(spec.Env)+5)
	for _, kv := range raw {
		key, _, _ := strings.Cut(kv, "=")
		configuredProviderCredential := false
		for _, providerKey := range spec.ProviderCredentialEnv {
			if strings.EqualFold(key, providerKey) {
				configuredProviderCredential = true
				break
			}
		}
		if credentialEnvKey(key) || configuredProviderCredential {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "VIRGIL_RUN_ID="+runID)
	if spec.GatewayURL != "" {
		env = append(env, "VIRGIL_GATEWAY_URL="+spec.GatewayURL)
		env = append(env, "OPENAI_BASE_URL="+spec.GatewayURL+"/v1")
		env = append(env, "OPENAI_API_BASE="+spec.GatewayURL+"/v1")
	}
	for key, value := range spec.Env {
		env = append(env, key+"="+value)
	}
	return env
}

func (p *ownedProcess) PID() int { return p.cmd.Process.Pid }

func (p *ownedProcess) Wait() ProcessResult {
	<-p.done
	return p.result
}

func (p *ownedProcess) Terminate(ctx context.Context) error {
	p.termOnce.Do(func() {
		terminateErr := p.platform.terminate(ctx, p.done)
		if terminateErr != nil {
			// Closing the job or killing the process group is a second tree-level
			// attempt. Kill the root as well so the owned Wait goroutine can reap it.
			p.closePlatform()
			killErr := p.cmd.Process.Kill()
			if errors.Is(killErr, os.ErrProcessDone) {
				killErr = nil
			}
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-p.done:
				p.termErr = errors.Join(terminateErr, p.closeErr, killErr)
			case <-timer.C:
				p.termErr = errors.Join(terminateErr, p.closeErr, killErr, errors.New("process reap timed out"))
			}
		} else {
			<-p.done
			p.termErr = p.closeErr
		}
		close(p.termDone)
	})
	<-p.termDone
	return p.termErr
}
