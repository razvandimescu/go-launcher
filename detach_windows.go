//go:build windows

package launcher

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts a process without a console and outside the caller's
// job, so it outlives the process that started it.
func startDetached(path string, args, env []string) (*os.Process, error) {
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	p, err := startWithFlags(path, args, env, flags|windows.CREATE_BREAKAWAY_FROM_JOB)
	if err != nil {
		// A job without JOB_OBJECT_LIMIT_BREAKAWAY_OK refuses the breakaway.
		p, err = startWithFlags(path, args, env, flags)
	}
	return p, err
}

func startWithFlags(path string, args, env []string, flags uint32) (*os.Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}
