package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const lockFileName = "launcher.lock"

var errLocked = errors.New("lock held by another process")

// lockfile is an OS file lock, which the kernel drops when its holder exits,
// however it exits. The PID written inside is for diagnostics only: checking
// it for liveness mistakes a reused PID, common after a reboot, for a running
// launcher.
type lockfile struct {
	path string
	f    *os.File
}

func newLockfile(dataDir string) *lockfile {
	return &lockfile{path: filepath.Join(dataDir, lockFileName)}
}

// Acquire attempts to take the lockfile. Returns an error if another
// launcher instance is already running.
func (l *lockfile) Acquire() error {
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("open lockfile: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLocked) {
			return fmt.Errorf("already running (pid %s)", lockHolder(l.path))
		}
		return fmt.Errorf("lock lockfile: %w", err)
	}
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}
	l.f = f
	return nil
}

// Release unlocks without deleting: a launcher waiting on the old file would
// lock an unlinked inode while a third one created and locked a new file.
func (l *lockfile) Release() {
	if l.f == nil {
		return
	}
	l.f.Close()
	l.f = nil
}

func lockHolder(path string) string {
	data, _ := os.ReadFile(path)
	if pid := strings.TrimSpace(string(data)); pid != "" {
		return pid
	}
	return "unknown"
}
