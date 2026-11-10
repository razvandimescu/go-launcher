//go:build !windows

package launcher

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetached starts a process in its own session, so it outlives the
// process that started it. Its standard streams go to the null device.
func startDetached(path string, args, env []string) (*os.Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}
