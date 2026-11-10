package launcher

import (
	"log/slog"
	"os"
	"os/exec"
	"time"
)

type childProcess struct {
	cmd       *exec.Cmd
	done      chan struct{}
	exitCode  int
	spawnTime time.Time
}

// spawnChild starts the child binary with the given args and env var.
func spawnChild(binaryPath string, args []string, envVar, dataDir string) (*childProcess, error) {
	cmd := exec.Command(binaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), envVar+"="+dataDir)

	spawnTime := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	cp := &childProcess{
		cmd:       cmd,
		done:      make(chan struct{}),
		spawnTime: spawnTime,
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				cp.exitCode = exitErr.ExitCode()
			} else {
				cp.exitCode = -1
			}
		}
		close(cp.done)
	}()

	return cp, nil
}

// Wait blocks until the child process exits.
func (cp *childProcess) Wait() {
	<-cp.done
}

// Done returns a channel that is closed when the child exits.
func (cp *childProcess) Done() <-chan struct{} {
	return cp.done
}

// ExitCode returns the child's exit code. Only valid after Done() is closed.
func (cp *childProcess) ExitCode() int {
	return cp.exitCode
}

// Runtime returns how long the child has been running (or ran).
func (cp *childProcess) Runtime() time.Duration {
	select {
	case <-cp.done:
		return cp.cmd.ProcessState.SystemTime() + cp.cmd.ProcessState.UserTime()
	default:
		return time.Since(cp.spawnTime)
	}
}

// WallRuntime returns the wall-clock time since the child was spawned.
func (cp *childProcess) WallRuntime() time.Duration {
	return time.Since(cp.spawnTime)
}

// Signal sends a signal to the child process.
func (cp *childProcess) Signal(sig os.Signal) error {
	if cp.cmd.Process == nil {
		return nil
	}
	return cp.cmd.Process.Signal(sig)
}

// Kill sends SIGKILL to the child process.
func (cp *childProcess) Kill() error {
	if cp.cmd.Process == nil {
		return nil
	}
	return cp.cmd.Process.Kill()
}

// terminateWithEscalation sends SIGTERM, waits for the timeout, then SIGKILL.
// Blocks until the process is fully dead.
func terminateWithEscalation(cp *childProcess, killTimeout time.Duration) {
	// Try graceful termination first
	if err := cp.Signal(os.Interrupt); err != nil {
		slog.Warn("failed to send interrupt to child", "error", err)
	}

	select {
	case <-cp.Done():
		return // exited gracefully
	case <-time.After(killTimeout):
		slog.Warn("child did not exit after timeout, sending SIGKILL", "timeout", killTimeout)
		if err := cp.Kill(); err != nil {
			slog.Error("failed to kill child", "error", err)
		}
		<-cp.Done() // wait for full termination
	}
}
