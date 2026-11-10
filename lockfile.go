package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const lockFileName = "launcher.lock"

type lockfile struct {
	path string
	// takenFrom is the parent's PID when Acquire took the lock over from it.
	takenFrom int
}

func newLockfile(dataDir string) *lockfile {
	return &lockfile{path: filepath.Join(dataDir, lockFileName)}
}

// Acquire attempts to take the lockfile. Returns an error if another
// launcher instance is already running. A lock held by the parent process is
// taken over: during a launcher update, each process hands the lock to the
// process it starts.
func (l *lockfile) Acquire() error {
	// Check for existing lock
	if data, err := os.ReadFile(l.path); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && processAlive(pid) {
			if pid != os.Getppid() {
				return fmt.Errorf("already running (pid %d)", pid)
			}
			l.takenFrom = pid
		}
		// Stale lockfile — previous launcher crashed
	}

	pid := os.Getpid()
	if err := os.WriteFile(l.path, []byte(strconv.Itoa(pid)), 0600); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}
	return nil
}

// held reports whether the lockfile holds this process's PID.
func (l *lockfile) held() bool {
	data, err := os.ReadFile(l.path)
	return err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(os.Getpid())
}

// Release removes the lockfile if this process holds it. A launcher that
// handed the lock over must not delete the new holder's lock.
func (l *lockfile) Release() {
	if l.held() {
		os.Remove(l.path)
	}
}
