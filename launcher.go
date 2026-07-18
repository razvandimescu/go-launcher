package launcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

var (
	defaultBackoff                = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}
	defaultCrashThreshold         = 3
	defaultProbationDuration      = 10 * time.Minute
	defaultKillTimeout            = 30 * time.Second
	defaultCrashWindow            = 5 * time.Minute
	defaultHeartbeatPoll          = 500 * time.Millisecond
	defaultUpdateFailureThreshold = 3
	defaultUpdateFailureWindow    = 5 * time.Minute
	defaultUpdateFailureCooldown  = 15 * time.Minute
)

// Config configures the launcher behavior.
type Config struct {
	// Required fields.
	AppName         string // display name
	ChildBinaryName string // binary filename inside versions/current/
	DataDir         string // state, versions, IPC files
	InstallDir      string // where the launcher binary should live
	EnvVarName      string // env var set on child process (e.g. "MYAPP_LAUNCHER_STATE_DIR")

	// LauncherBinaryName overrides the filename used when relocating the
	// launcher binary into InstallDir. Defaults to the basename of the
	// currently running executable, which is brittle: a binary launched as
	// MyApp-1.0-installer.exe lands at InstallDir/MyApp-1.0-installer.exe
	// permanently. Set this to a stable name (e.g. "MyApp.exe") for a
	// deterministic install location.
	LauncherBinaryName string

	// Optional with sensible defaults.
	ChildArgs         []string        // args forwarded to child (default: none)
	Backoff           []time.Duration // restart delays (default: [2s, 5s, 15s])
	CrashThreshold    int             // crashes before rollback (default: 3)
	CrashWindow       time.Duration   // crash count resets after this duration (default: 5min)
	ProbationDuration time.Duration   // new version probation (default: 10min)
	KillTimeout       time.Duration   // SIGTERM → SIGKILL escalation (default: 30s)
	HeartbeatPoll     time.Duration   // heartbeat check interval (default: 500ms)

	// UpdateFailureThreshold, UpdateFailureWindow, and UpdateFailureCooldown
	// govern a persistently failing update source (e.g. a firewall blocking
	// the download indefinitely) — without them, a child that requests the
	// same unreachable update on every startup would spin the launcher in a
	// tight, unbounded restart loop that never trips the crash/rollback
	// safety net (a failed download never touches CurrentVersion, so it
	// isn't tracked as a crash). After UpdateFailureThreshold consecutive
	// failed update attempts within UpdateFailureWindow, the launcher stops
	// attempting the update (running the working current version normally
	// instead) for UpdateFailureCooldown before trying again.
	UpdateFailureThreshold int           // failed attempts before cooldown (default: 3)
	UpdateFailureWindow    time.Duration // window for counting consecutive failures (default: 5min)
	UpdateFailureCooldown  time.Duration // pause on further attempts after threshold (default: 15min)

	// Pluggable components.
	UI        UI        // nil = headless
	Fetcher   Fetcher   // nil = no bootstrap/updates
	Registrar Registrar // nil = skip OS registration

	// AfterLockAcquired runs once the launcher has won the singleton
	// lockfile race and before any bootstrap or supervisor work. Use it
	// for one-time setup that must only execute on the actively
	// supervising process — legacy cleanup, migration tasks, registry
	// changes that should not race a concurrent launcher invocation.
	// A non-nil error from the hook causes Run to return 1.
	AfterLockAcquired func(ctx context.Context) error

	// RelaunchArgs transforms the arguments forwarded to the relocated
	// launcher copy after self-relocation. If nil, os.Args[1:] is
	// forwarded verbatim. Return nil or an empty slice to drop all
	// arguments — useful when the launcher was invoked through a legacy
	// installer protocol whose arguments should not propagate.
	RelaunchArgs func(args []string) []string
}

// Launcher supervises a child process with versioned deployments and rollback.
type Launcher struct {
	cfg   Config
	state *State
	lock  *lockfile
}

// New creates a Launcher with the given config, applying defaults for unset fields.
func New(cfg Config) *Launcher {
	if len(cfg.Backoff) == 0 {
		cfg.Backoff = defaultBackoff
	}
	if cfg.CrashThreshold == 0 {
		cfg.CrashThreshold = defaultCrashThreshold
	}
	if cfg.CrashWindow == 0 {
		cfg.CrashWindow = defaultCrashWindow
	}
	if cfg.ProbationDuration == 0 {
		cfg.ProbationDuration = defaultProbationDuration
	}
	if cfg.KillTimeout == 0 {
		cfg.KillTimeout = defaultKillTimeout
	}
	if cfg.HeartbeatPoll == 0 {
		cfg.HeartbeatPoll = defaultHeartbeatPoll
	}
	if cfg.UpdateFailureThreshold == 0 {
		cfg.UpdateFailureThreshold = defaultUpdateFailureThreshold
	}
	if cfg.UpdateFailureWindow == 0 {
		cfg.UpdateFailureWindow = defaultUpdateFailureWindow
	}
	if cfg.UpdateFailureCooldown == 0 {
		cfg.UpdateFailureCooldown = defaultUpdateFailureCooldown
	}
	return &Launcher{cfg: cfg}
}

func (l *Launcher) runAfterLockHook(ctx context.Context) error {
	if l.cfg.AfterLockAcquired == nil {
		return nil
	}
	if err := l.cfg.AfterLockAcquired(ctx); err != nil {
		slog.Error("AfterLockAcquired hook failed", "error", err)
		return err
	}
	return nil
}

// Status returns the current launcher state (for --status flag).
func (l *Launcher) Status() *State {
	if l.state == nil {
		s, err := loadState(l.cfg.DataDir)
		if err != nil {
			return &State{}
		}
		return s
	}
	return l.state
}

// Run executes the main supervisor loop. Returns the process exit code.
func (l *Launcher) Run(ctx context.Context) int {
	// Self-relocate on first run
	if l.cfg.InstallDir != "" {
		relocated, err := selfRelocate(l.cfg.InstallDir, l.cfg.LauncherBinaryName, l.cfg.RelaunchArgs)
		if err != nil {
			slog.Error("self-relocation failed (continuing from current location)", "error", err)
		}
		if relocated {
			return 0
		}
	}

	l.registerLoginItem()

	// Ensure data directory exists
	if err := ensureDataDir(l.cfg.DataDir); err != nil {
		slog.Error("failed to create data directory", "error", err)
		return 1
	}

	// Persist logs to a file now that DataDir exists — stderr is lost on a
	// console-less GUI/login-item process, so this is the only record of
	// bootstrap/update failures.
	closeLog := setupFileLogging(filepath.Join(l.cfg.DataDir, "launcher.log"))
	defer closeLog()

	// Acquire singleton lock
	l.lock = newLockfile(l.cfg.DataDir)
	if err := l.lock.Acquire(); err != nil {
		slog.Error("failed to acquire lockfile", "error", err)
		return 1
	}
	defer l.lock.Release()

	if err := l.runAfterLockHook(ctx); err != nil {
		return 1
	}

	// Load state
	var err error
	l.state, err = loadState(l.cfg.DataDir)
	if err != nil {
		slog.Error("failed to load state", "error", err)
		return 1
	}

	// Startup recovery
	deleteStateTmpFiles(l.cfg.DataDir)
	cleanStagingDir(l.cfg.DataDir)
	recoverInterruptedSwap(l.cfg.DataDir)

	l.expireStaleCrashStateAtStartup()

	// Ensure version directories exist
	if err := ensureVersionDirs(l.cfg.DataDir); err != nil {
		slog.Error("failed to create version directories", "error", err)
		return 1
	}

	// Bootstrap download if no current version
	if !hasCurrentVersion(l.cfg.DataDir) {
		if l.cfg.Fetcher == nil {
			slog.Error("no child binary and no fetcher configured")
			if l.cfg.UI != nil {
				l.cfg.UI.ShowError("No application found. Please reinstall.")
			}
			return 1
		}

		if l.cfg.UI != nil {
			l.cfg.UI.ShowSplash("Setting up " + l.cfg.AppName + "...")
		}

		if err := bootstrapDownload(ctx, &l.cfg); err != nil {
			slog.Error("bootstrap download failed", "error", err)
			if l.cfg.UI != nil {
				l.cfg.UI.ShowError("Setup failed: " + err.Error())
			}
			return 1
		}
	}

	return l.supervisorLoop(ctx)
}

func (l *Launcher) supervisorLoop(ctx context.Context) int {
	crashIndex := 0 // index into backoff array

	for {
		// Check crash threshold
		action, code := l.handleCrashThreshold()
		switch action {
		case actionRolledBack:
			crashIndex = 0
			continue
		case actionFatal:
			return code
		}

		// Delete old heartbeat before spawning
		deleteHeartbeat(l.cfg.DataDir)

		if l.cfg.UI != nil {
			msg := "Starting " + l.cfg.AppName
			if l.state.CurrentVersion != "" {
				msg += " " + l.state.CurrentVersion
			}
			l.cfg.UI.ShowSplash(msg + "...")
		}

		binaryPath := childBinaryPath(l.cfg.DataDir, l.cfg.ChildBinaryName)
		child, err := spawnChild(binaryPath, l.cfg.ChildArgs, l.cfg.EnvVarName, l.cfg.DataDir)
		if err != nil {
			slog.Error("failed to spawn child", "path", binaryPath, "error", err)
			l.recordCrash()
			crashIndex = l.sleepBackoff(ctx, crashIndex)
			continue
		}

		slog.Info("child spawned", "pid", child.cmd.Process.Pid, "path", binaryPath)

		// Wait for child to exit, monitoring heartbeat
		exitCode := l.waitForChild(ctx, child)
		l.checkProbation(child)

		// Launcher itself is shutting down (SIGINT/SIGTERM) — exit cleanly
		if ctx.Err() != nil {
			slog.Info("launcher shutting down", "child_exit_code", exitCode)
			return 0
		}

		// Handle exit
		if shutdownRequested(l.cfg.DataDir) {
			switch l.handleUpdate(ctx) {
			case updateApplied:
				crashIndex = 0
				continue
			case updateNotApplied:
				// Failed, or skipped due to an active cooldown — back off
				// same as a crash would, so a persistently failing update
				// source can't spin this into a tight restart loop.
				crashIndex = l.sleepBackoff(ctx, crashIndex)
				continue
			}
			slog.Info("child requested clean shutdown")
			deleteShutdownFile(l.cfg.DataDir)
			return 0
		}

		// No shutdown_requested — treat as crash regardless of exit code
		slog.Warn("child exited unexpectedly", "exit_code", exitCode,
			"runtime", child.WallRuntime())
		deletePendingUpdate(l.cfg.DataDir)
		l.recordCrash()
		crashIndex = l.sleepBackoff(ctx, crashIndex)
	}
}

type crashAction int

const (
	actionContinue   crashAction = iota // below threshold, proceed
	actionRolledBack                    // rollback performed, restart loop
	actionFatal                         // unrecoverable, exit
)

// updateAction is handleUpdate's result, mirroring the crashAction idiom
// above: a named outcome reads more clearly at the call site than a tuple
// of bools whose combinations aren't all meaningful.
type updateAction int

const (
	updateNone       updateAction = iota // no pending update was found
	updateApplied                        // downloaded and installed successfully
	updateNotApplied                     // failed, or skipped due to an active cooldown
)

func (l *Launcher) handleCrashThreshold() (crashAction, int) {
	if l.state.CrashCount < l.cfg.CrashThreshold {
		return actionContinue, 0
	}

	// RolledBackFrom == "" means we have never rolled back, so a previous
	// version on disk is fair game. Without that clause an unknown-version
	// install (manually seeded, or bootstrapped with no recorded version)
	// has RolledBackFrom == PreviousVersion == "" and the guard would
	// wrongly skip a perfectly good previous/ binary, exiting forever.
	rollbackAllowed := l.state.RolledBackFrom == "" || l.state.RolledBackFrom != l.state.PreviousVersion
	if hasPreviousVersion(l.cfg.DataDir) && rollbackAllowed {
		slog.Warn("crash threshold reached, rolling back",
			"crash_count", l.state.CrashCount,
			"from", l.state.CurrentVersion,
			"to", l.state.PreviousVersion)

		if l.cfg.UI != nil {
			l.cfg.UI.ShowSplash("Recovering...")
		}

		if err := rollback(l.cfg.DataDir); err != nil {
			slog.Error("rollback failed", "error", err)
			return actionFatal, 1
		}

		l.state.RolledBackFrom = l.state.CurrentVersion
		l.state.CurrentVersion, l.state.PreviousVersion = l.state.PreviousVersion, l.state.CurrentVersion
		l.state.CrashCount = 0
		l.state.CrashWindowStart = time.Time{}
		l.state.ProbationUntil = time.Time{}

		if err := saveState(l.cfg.DataDir, l.state); err != nil {
			slog.Error("failed to save state after rollback", "error", err)
		}
		return actionRolledBack, 0
	}

	slog.Error("crash loop with no viable rollback target",
		"crash_count", l.state.CrashCount)
	if l.cfg.UI != nil {
		l.cfg.UI.ShowError(l.cfg.AppName + " is unable to start. Please reinstall or contact support.")
	}
	return actionFatal, 1
}

func (l *Launcher) checkProbation(child *childProcess) {
	if l.state.ProbationUntil.IsZero() {
		return
	}
	if !heartbeatExists(l.cfg.DataDir) {
		return
	}
	if child.WallRuntime() <= l.cfg.ProbationDuration {
		return
	}

	slog.Info("version survived probation, marking stable",
		"version", l.state.CurrentVersion,
		"runtime", child.WallRuntime())

	l.state.resetCrashState()
	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Error("failed to save state after probation clear", "error", err)
	}
}

// handleUpdate checks for a pending update after shutdown was requested.
func (l *Launcher) handleUpdate(ctx context.Context) updateAction {
	pending := readPendingUpdate(l.cfg.DataDir)
	if pending == nil {
		return updateNone
	}
	// Every path below clears the same two marker files exactly once,
	// regardless of outcome — deferred so each branch only states its own
	// distinguishing logic.
	defer deletePendingUpdate(l.cfg.DataDir)
	defer deleteShutdownFile(l.cfg.DataDir)

	slog.Info("child requested update", "version", pending.Version)

	if !l.state.UpdateCooldownUntil.IsZero() && time.Now().Before(l.state.UpdateCooldownUntil) {
		slog.Warn("skipping update attempt — in cooldown after repeated failures",
			"version", pending.Version,
			"cooldown_until", l.state.UpdateCooldownUntil)
		return updateNotApplied
	}

	if l.cfg.UI != nil {
		l.cfg.UI.ShowSplash("Updating to " + pending.Version + "...")
	}

	if err := performUpdate(ctx, &l.cfg, pending); err != nil {
		slog.Error("update failed, restarting current version", "error", err)
		cleanStagingDir(l.cfg.DataDir)
		l.recordUpdateFailure()
		return updateNotApplied
	}

	l.state.PreviousVersion = l.state.CurrentVersion
	l.state.CurrentVersion = pending.Version
	l.state.resetCrashState()
	l.state.resetUpdateFailureState()
	l.state.ProbationUntil = time.Now().Add(l.cfg.ProbationDuration)
	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Error("failed to save state after update", "error", err)
	}

	return updateApplied
}

// waitForChild waits for the child to exit, monitoring the heartbeat file
// to dismiss the splash screen.
func (l *Launcher) waitForChild(ctx context.Context, cp *childProcess) int {
	heartbeatChecked := false
	ticker := time.NewTicker(l.cfg.HeartbeatPoll)
	defer ticker.Stop()

	for {
		select {
		case <-cp.Done():
			return cp.ExitCode()
		case <-ticker.C:
			if !heartbeatChecked && heartbeatExists(l.cfg.DataDir) {
				heartbeatChecked = true
				ticker.Stop()
				slog.Info("child is healthy (heartbeat detected)")
				l.state.LastHealthyAt = time.Now()
				saveState(l.cfg.DataDir, l.state)
				if l.cfg.UI != nil {
					l.cfg.UI.HideSplash()
				}
			}
		case <-ctx.Done():
			slog.Info("context cancelled, terminating child")
			terminateWithEscalation(cp, l.cfg.KillTimeout)
			return cp.ExitCode()
		}
	}
}

// expireStaleWindow resets a count+windowStart pair (crash tracking and
// update-failure tracking each keep one) if more than window has elapsed
// since the first recorded event. Returns true if state was changed. A
// shared free function rather than two copy-pasted methods — the two
// trackers' *remediation* differs (rollback vs. cooldown) enough to stay
// separate, but the "expire after a quiet window" bookkeeping underneath
// is identical.
func expireStaleWindow(count *int, windowStart *time.Time, window time.Duration, now time.Time) bool {
	if windowStart.IsZero() || now.Sub(*windowStart) <= window {
		return false
	}
	*count, *windowStart = 0, time.Time{}
	return true
}

func (l *Launcher) recordCrash() {
	now := time.Now()
	expireStaleWindow(&l.state.CrashCount, &l.state.CrashWindowStart, l.cfg.CrashWindow, now)

	l.state.CrashCount++
	if l.state.CrashWindowStart.IsZero() {
		l.state.CrashWindowStart = now
	}
	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Error("failed to save state after crash", "error", err)
	}
}

// expireStaleCrashStateAtStartup clears a saturated crash_count carried over
// from a prior session — needed because handleCrashThreshold runs before any
// new crash, so recordCrash's window-expiry never gets a chance.
func (l *Launcher) expireStaleCrashStateAtStartup() {
	if !expireStaleWindow(&l.state.CrashCount, &l.state.CrashWindowStart, l.cfg.CrashWindow, time.Now()) {
		return
	}
	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Warn("failed to persist crash window reset", "error", err)
	}
}

// recordUpdateFailure tracks a failed update download, separately from
// crash tracking (see the State.UpdateFailureCount doc comment). Once
// UpdateFailureThreshold consecutive failures land within
// UpdateFailureWindow, it sets UpdateCooldownUntil so handleUpdate skips
// further attempts for UpdateFailureCooldown instead of retrying the same
// unreachable source on every restart.
func (l *Launcher) recordUpdateFailure() {
	now := time.Now()
	expireStaleWindow(&l.state.UpdateFailureCount, &l.state.UpdateFailureWindowStart, l.cfg.UpdateFailureWindow, now)

	l.state.UpdateFailureCount++
	if l.state.UpdateFailureWindowStart.IsZero() {
		l.state.UpdateFailureWindowStart = now
	}

	if l.state.UpdateFailureCount >= l.cfg.UpdateFailureThreshold {
		l.state.UpdateCooldownUntil = now.Add(l.cfg.UpdateFailureCooldown)
		slog.Warn("update failure threshold reached, pausing update attempts",
			"failures", l.state.UpdateFailureCount,
			"cooldown_until", l.state.UpdateCooldownUntil)
	}

	if err := saveState(l.cfg.DataDir, l.state); err != nil {
		slog.Error("failed to save state after update failure", "error", err)
	}
}

func (l *Launcher) sleepBackoff(ctx context.Context, index int) int {
	delay := l.cfg.Backoff[index]
	slog.Info("waiting before restart", "delay", delay, "crash_count", l.state.CrashCount)

	select {
	case <-time.After(delay):
	case <-ctx.Done():
	}

	// Advance index, cap at last element
	if index < len(l.cfg.Backoff)-1 {
		return index + 1
	}
	return index
}

// Executable returns the path to the child binary in versions/current/.
func (l *Launcher) Executable() string {
	return childBinaryPath(l.cfg.DataDir, l.cfg.ChildBinaryName)
}

// DataDir returns the configured data directory.
func (l *Launcher) DataDir() string {
	return l.cfg.DataDir
}

// registerLoginItem registers the launcher as an OS login item.
// Idempotent and non-fatal — logs a warning on failure.
func (l *Launcher) registerLoginItem() {
	if l.cfg.Registrar == nil {
		return
	}
	execPath, err := os.Executable()
	if err != nil {
		slog.Warn("cannot determine executable path for registration", "error", err)
		return
	}
	if err := l.cfg.Registrar.RegisterLoginItem(execPath); err != nil {
		slog.Warn("login item registration failed", "error", err)
	}
}

// Shutdown writes shutdown_requested and can be called from the child
// to coordinate a clean exit. This is primarily for testing; in production,
// the child uses the child/ package.
func Shutdown(dataDir string) error {
	return writeShutdownRequested(dataDir)
}

// RequestUpdate writes pending_update.json and shutdown_requested.
// This is primarily for testing; in production, the child uses the child/ package.
func RequestUpdate(dataDir string, r *Release) error {
	if err := writePendingUpdate(dataDir, r); err != nil {
		return err
	}
	return writeShutdownRequested(dataDir)
}
