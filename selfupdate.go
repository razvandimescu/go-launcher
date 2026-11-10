package launcher

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A launcher started with replacePIDEnv set to its parent's PID runs as the
// update helper. The parent puts the announced version in replaceVersionEnv.
const (
	replacePIDEnv     = "GO_LAUNCHER_REPLACE_PID"
	replaceVersionEnv = "GO_LAUNCHER_REPLACE_VERSION"
)

// The design of the launcher self-update is in docs/launcher-self-update.md.

// launcherCheckTimeout bounds the LatestLauncher call, so a slow endpoint
// cannot hold back the child's start.
const launcherCheckTimeout = 30 * time.Second

// checkLauncherUpdate stages a launcher release that differs from
// cfg.Version at <executable>.new and hands it to the update helper. It
// reports whether this process must exit because a new launcher took over.
// Every failure is logged: the child starts as it would without the check.
// A launcher started by the update helper runs no check: it waits for the
// helper's decision instead.
func (l *Launcher) checkLauncherUpdate(ctx context.Context) bool {
	if l.lock.takenFrom != 0 {
		l.waitForUpdateHelper()
		return false
	}
	lf, ok := l.cfg.Fetcher.(LauncherFetcher)
	if !ok || l.cfg.Version == "" {
		return false
	}
	execPath, err := os.Executable()
	if err == nil {
		execPath, err = filepath.EvalSymlinks(execPath)
	}
	if err != nil {
		slog.Warn("launcher update check skipped: executable path unknown", "error", err)
		return false
	}
	// Replacing the binary inside a bundle would break its code signature.
	if strings.Contains(execPath, ".app/") {
		slog.Info("launcher update check skipped inside an .app bundle", "path", execPath)
		return false
	}
	deleteLauncherLeftovers(execPath)
	release := stageLauncherUpdate(ctx, &l.cfg, lf, l.state.FailedLauncherVersion, execPath+".new")
	if release == nil {
		return false
	}
	return l.runLauncherUpdate(execPath, release)
}

// stageLauncherUpdate downloads the latest launcher to dest when its version
// differs from cfg.Version, older included: an older answer means the newer
// release was withdrawn. A release equal to failed is skipped. It returns the
// staged release, or nil.
func stageLauncherUpdate(ctx context.Context, cfg *Config, lf LauncherFetcher, failed, dest string) *Release {
	checkCtx, cancel := context.WithTimeout(ctx, launcherCheckTimeout)
	release, err := lf.LatestLauncher(checkCtx)
	cancel()
	if err != nil {
		slog.Warn("launcher update check failed", "error", err)
		return nil
	}
	if sameVersion(release.Version, cfg.Version) {
		slog.Info("launcher up to date", "version", cfg.Version)
		return nil
	}
	if failed != "" && sameVersion(release.Version, failed) {
		slog.Info("launcher update skipped: this version failed before", "version", release.Version)
		return nil
	}
	// The launcher replaces itself, so unlike a child update it never runs unverified.
	if algo, _, ok := parseChecksum(release.Checksum); !ok || algo != "sha256" {
		slog.Warn("launcher update skipped: release has no sha256 checksum", "version", release.Version)
		return nil
	}
	if cfg.UI != nil {
		cfg.UI.ShowSplash("Updating launcher to " + release.Version + "...")
	}
	if err := downloadVerified(ctx, cfg, release, dest); err != nil {
		slog.Warn("launcher update download failed", "version", release.Version, "error", err)
		return nil
	}
	slog.Info("launcher update staged", "from", cfg.Version, "to", release.Version, "path", dest)
	return release
}

// sameVersion compares versions, ignoring a leading "v".
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// runLauncherUpdate starts the staged launcher as the update helper and keeps
// this launcher running until the helper ends. On success the helper stops
// this process, so returning means the update did not succeed, unless a new
// launcher holds the lock: then it returns true and this process must exit.
func (l *Launcher) runLauncherUpdate(execPath string, release *Release) bool {
	if l.state.FailedLauncherVersion != "" {
		l.recordFailedLauncher("")
	}
	env := append(os.Environ(),
		replacePIDEnv+"="+strconv.Itoa(os.Getpid()),
		replaceVersionEnv+"="+release.Version)
	helper, err := startDetached(execPath+".new", os.Args[1:], env)
	if err != nil {
		slog.Warn("launcher update helper did not start", "version", release.Version, "error", err)
		l.recordFailedLauncher(release.Version)
		return false
	}
	slog.Info("launcher update handed to the helper", "pid", helper.Pid, "version", release.Version)
	helperDone := processDone(helper)
	select {
	case <-helperDone:
	case <-time.After(6 * l.cfg.LauncherUpdateTimeout):
		slog.Error("launcher update helper did not end, stopping it", "pid", helper.Pid)
		helper.Kill()
		<-helperDone
	}

	if !l.lock.held() {
		if err := l.lock.Acquire(); err != nil {
			slog.Info("a new launcher took over, exiting", "error", err)
			return true
		}
	}
	if err := restoreOldLauncher(execPath, l.cfg.LauncherUpdateTimeout); err != nil {
		slog.Error("could not put the old launcher back", "error", err)
	}
	os.Remove(execPath + ".bad")
	if s, err := loadState(l.cfg.DataDir); err == nil {
		l.state = s
	}
	if l.state.FailedLauncherVersion != release.Version {
		l.recordFailedLauncher(release.Version)
	}
	slog.Warn("launcher update did not complete, continuing with this launcher", "version", release.Version)
	return false
}

// runHelper performs the launcher update as the supervisor: it swaps the
// files, starts the new launcher and checks it. If the new launcher is
// healthy, it stops the old launcher and cleans up; if not, it puts the old
// file back and the old launcher continues.
func (l *Launcher) runHelper(oldPID int) int {
	if err := ensureDataDir(l.cfg.DataDir); err != nil {
		slog.Error("launcher update helper: data directory unavailable", "error", err)
		return 1
	}
	closeLog := setupFileLogging(filepath.Join(l.cfg.DataDir, "launcher.log"))
	defer closeLog()

	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	exe, staged := strings.CutSuffix(self, ".new")
	if err != nil || !staged {
		slog.Error("launcher update helper is not a staged launcher", "path", self, "error", err)
		return 1
	}
	l.lock = newLockfile(l.cfg.DataDir)
	defer l.lock.Release()
	if err := l.lock.Acquire(); err != nil || l.lock.takenFrom != oldPID {
		slog.Error("launcher update helper could not take the lock from the old launcher", "error", err)
		return 1
	}
	if l.state, err = loadState(l.cfg.DataDir); err != nil {
		slog.Error("launcher update helper could not load the state", "error", err)
		return 1
	}

	limit := time.AfterFunc(5*l.cfg.LauncherUpdateTimeout, func() {
		slog.Error("launcher update helper reached its time limit")
		os.Exit(1)
	})
	defer limit.Stop()

	announced := os.Getenv(replaceVersionEnv)
	if !sameVersion(l.cfg.Version, announced) {
		slog.Error("staged launcher reports another version than announced", "announced", announced, "staged", l.cfg.Version)
		l.recordFailedLauncher(announced)
		return 1
	}
	return l.swapLauncher(exe, oldPID)
}

// swapLauncher replaces exe with the staged launcher, starts it and waits for
// its launcher_ready. Every failure, a panic included, undoes the swap.
func (l *Launcher) swapLauncher(exe string, oldPID int) (code int) {
	timeout := l.cfg.LauncherUpdateTimeout
	var newLauncher *os.Process
	var newDone <-chan struct{}
	abort := func(reason string) int {
		if newLauncher != nil {
			newLauncher.Kill()
			<-newDone
		}
		if err := restoreOldLauncher(exe, timeout); err != nil {
			slog.Error("could not put the old launcher back", "error", err)
		}
		l.recordFailedLauncher(l.cfg.Version)
		slog.Error("new version couldn't start, version upgrade aborted", "version", l.cfg.Version, "reason", reason)
		return 1
	}
	defer func() {
		if r := recover(); r != nil {
			code = abort(fmt.Sprint("panic: ", r))
		}
	}()

	if err := os.Remove(exe + ".old"); err != nil && !os.IsNotExist(err) {
		return abort(err.Error())
	}
	// A second name keeps the old file, so exe is replaced in one step and never missing.
	if err := os.Link(exe, exe+".old"); err != nil {
		return abort(err.Error())
	}
	if err := renameWithRetry(exe+".new", exe, timeout); err != nil {
		return abort(err.Error())
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, replacePIDEnv+"=") || strings.HasPrefix(kv, replaceVersionEnv+"=")
	})
	newLauncher, err := startDetached(exe, os.Args[1:], env)
	if err != nil {
		return abort(err.Error())
	}
	newDone = processDone(newLauncher)
	if !l.waitLauncherReady(newLauncher.Pid, newDone) {
		return abort("no launcher_ready from the new launcher")
	}

	if old, err := os.FindProcess(oldPID); err == nil {
		old.Kill()
	}
	if !waitFor(timeout, func() bool { return !processAlive(oldPID) }) {
		slog.Warn("old launcher did not stop", "pid", oldPID)
	}
	if !waitFor(timeout, func() bool { err = os.Remove(exe + ".old"); return err == nil || os.IsNotExist(err) }) {
		slog.Warn("could not delete the old launcher", "error", err)
	}
	slog.Info("launcher updated", "version", l.cfg.Version, "pid", newLauncher.Pid)
	return 0
}

// waitLauncherReady reports whether the launcher with the given PID wrote
// launcher_ready with this launcher's version before it ended or timed out.
func (l *Launcher) waitLauncherReady(pid int, done <-chan struct{}) bool {
	ready := false
	waitFor(l.cfg.LauncherUpdateTimeout, func() bool {
		select {
		case <-done:
			return true
		default:
		}
		r := readLauncherReady(l.cfg.DataDir)
		ready = r != nil && r.PID == pid && sameVersion(r.Version, l.cfg.Version)
		return ready
	})
	return ready
}

// waitForUpdateHelper holds a launcher that took the lock over from the
// update helper until the helper has decided. Otherwise a rollback could
// start a second child next to this launcher's child.
func (l *Launcher) waitForUpdateHelper() {
	helper := l.lock.takenFrom
	if !waitFor(2*l.cfg.LauncherUpdateTimeout, func() bool {
		return os.Getppid() != helper || !processAlive(helper)
	}) {
		slog.Warn("launcher update helper is still running, continuing", "pid", helper)
	}
}

// recordFailedLauncher saves the launcher release whose update failed.
func (l *Launcher) recordFailedLauncher(version string) {
	l.state.FailedLauncherVersion = version
	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Warn("failed to save the failed launcher version", "error", err)
	}
}

// updateHelperParent returns the old launcher's PID when this process was
// started as the update helper. The variable counts only when it names the
// parent, so a stale or inherited value does nothing.
func updateHelperParent() (int, bool) {
	pid, err := strconv.Atoi(os.Getenv(replacePIDEnv))
	return pid, err == nil && pid == os.Getppid()
}

// restoreOldLauncher puts exe.old back at exe in one step and keeps the
// replaced file as exe.bad. It does nothing when no exe.old exists.
func restoreOldLauncher(exe string, timeout time.Duration) error {
	old, err := os.Stat(exe + ".old")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur, err := os.Stat(exe); err == nil {
		// The swap stopped after the link: exe is still the old file.
		if os.SameFile(cur, old) {
			return os.Remove(exe + ".old")
		}
		// Windows replaces a running file only when it has a second name.
		os.Remove(exe + ".bad")
		if err := os.Link(exe, exe+".bad"); err != nil {
			slog.Warn("could not keep the failed launcher", "error", err)
		}
	}
	return renameWithRetry(exe+".old", exe, timeout)
}

// deleteLauncherLeftovers removes the files of an earlier launcher update. An
// exe.old means that update did not reach its last step.
func deleteLauncherLeftovers(exe string) {
	if _, err := os.Stat(exe + ".old"); err == nil {
		slog.Warn("found " + filepath.Base(exe) + ".old: the previous launcher update did not finish its last step (cleanup + clean exit)")
	}
	os.Remove(exe + ".old")
	os.Remove(exe + ".bad")
}

// renameWithRetry repeats a rename until it succeeds or the timeout passes:
// on Windows the system or a virus scanner can hold a file for a moment.
func renameWithRetry(src, dst string, timeout time.Duration) error {
	var err error
	waitFor(timeout, func() bool {
		err = os.Rename(src, dst)
		return err == nil
	})
	return err
}

// waitFor polls cond until it is true or the timeout passes, and reports
// whether it became true.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

// processDone returns a channel that is closed when the child process p ends.
func processDone(p *os.Process) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		p.Wait()
		close(done)
	}()
	return done
}
