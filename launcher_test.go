package launcher

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// env-var-driven child source used by the legacy integration tests.
const envVarChildSource = `package main

import (
	"os"
	"time"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")

	if os.Getenv("CHILD_HEARTBEAT") == "1" && stateDir != "" {
		f, _ := os.Create(stateDir + "/heartbeat")
		f.Close()
	}

	if os.Getenv("CHILD_SHUTDOWN") == "1" && stateDir != "" {
		os.WriteFile(stateDir + "/shutdown_requested", []byte(""), 0600)
	}

	if d := os.Getenv("CHILD_SLEEP"); d != "" {
		dur, _ := time.ParseDuration(d)
		time.Sleep(dur)
	}

	if os.Getenv("CHILD_EXIT_CODE") == "1" {
		os.Exit(1)
	}
}
`

// baseTestConfig returns a Config with fast defaults suitable for tests.
func baseTestConfig(binName, dataDir string) Config {
	return Config{
		AppName:         "test-app",
		ChildBinaryName: binName,
		DataDir:         dataDir,
		EnvVarName:      "TEST_LAUNCHER_STATE_DIR",
		Backoff:         []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
		CrashThreshold:  3,
		CrashWindow:     10 * time.Second,
		KillTimeout:     2 * time.Second,
		HeartbeatPoll:   50 * time.Millisecond,
	}
}

func setupTestLauncher(t *testing.T) (cfg Config, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	binDir, binName := buildChildFromSource(t, envVarChildSource)
	installChild(t, binDir, binName, dataDir, "current")

	cfg = baseTestConfig(binName, dataDir)
	cfg.Backoff = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	cfg.ProbationDuration = 500 * time.Millisecond

	return cfg, dataDir
}

func TestCleanShutdown(t *testing.T) {
	cfg, _ := setupTestLauncher(t)

	// Child will touch heartbeat and write shutdown_requested
	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_EXIT_CODE", "0")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestCrashLoopExits(t *testing.T) {
	cfg, _ := setupTestLauncher(t)

	t.Setenv("CHILD_EXIT_CODE", "1")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 1 {
		t.Errorf("expected exit code 1 after crash loop, got %d", code)
	}

	// Verify crash count reached threshold
	state, _ := loadState(cfg.DataDir)
	if state.CrashCount < cfg.CrashThreshold {
		t.Errorf("expected crash count >= %d, got %d", cfg.CrashThreshold, state.CrashCount)
	}
}

func TestStaleCrashStateExpiresAtStartup(t *testing.T) {
	// Regression: a saturated crash_count from a previous session (test
	// machine where users repeatedly close the runner) used to permanently
	// brick the launcher. Loading state past CrashWindow must reset the counter.
	cfg, dataDir := setupTestLauncher(t)

	saveState(dataDir, &State{
		CurrentVersion:   "1.0.0",
		CrashCount:       cfg.CrashThreshold,
		CrashWindowStart: time.Now().Add(-2 * cfg.CrashWindow),
	})

	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_EXIT_CODE", "0")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if code := l.Run(ctx); code != 0 {
		t.Errorf("expected exit 0 after stale-crash recovery, got %d", code)
	}

	state, _ := loadState(dataDir)
	if state.CrashCount != 0 {
		t.Errorf("expected crash count cleared, got %d", state.CrashCount)
	}
	if !state.CrashWindowStart.IsZero() {
		t.Errorf("expected crash window zeroed, got %v", state.CrashWindowStart)
	}
}

func TestRestartOnUnexpectedExit0(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)

	// Child exits 0 without shutdown_requested — should be treated as crash
	t.Setenv("CHILD_EXIT_CODE", "0")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}

	state, _ := loadState(dataDir)
	if state.CrashCount < cfg.CrashThreshold {
		t.Errorf("expected crash count >= %d, got %d", cfg.CrashThreshold, state.CrashCount)
	}
}

func TestLockfilePreventsDoubleStart(t *testing.T) {
	dataDir := t.TempDir()

	lf := newLockfile(dataDir)
	if err := lf.Acquire(); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer lf.Release()

	lf2 := newLockfile(dataDir)
	err := lf2.Acquire()
	if err == nil {
		t.Error("expected second acquire to fail")
	}
}

func TestStaleLockfileRecovery(t *testing.T) {
	dataDir := t.TempDir()

	// Write a lockfile with a non-existent PID
	lockPath := filepath.Join(dataDir, lockFileName)
	os.WriteFile(lockPath, []byte("999999999"), 0600)

	lf := newLockfile(dataDir)
	if err := lf.Acquire(); err != nil {
		t.Fatalf("should acquire stale lockfile, got: %v", err)
	}
	lf.Release()
}

func TestStateAtomicWriteAndLoad(t *testing.T) {
	dataDir := t.TempDir()

	s := &State{
		CurrentVersion:  "1.0.0",
		PreviousVersion: "0.9.0",
		CrashCount:      2,
	}

	if err := saveState(dataDir, s); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadState(dataDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.CurrentVersion != "1.0.0" || loaded.PreviousVersion != "0.9.0" || loaded.CrashCount != 2 {
		t.Errorf("loaded state mismatch: %+v", loaded)
	}
}

func TestCorruptedStateReturnsDefaults(t *testing.T) {
	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, stateFileName), []byte("not json"), 0600)

	s, err := loadState(dataDir)
	if err != nil {
		t.Fatalf("expected no error on corrupt state, got: %v", err)
	}
	if s.CurrentVersion != "" || s.CrashCount != 0 {
		t.Errorf("expected empty defaults, got: %+v", s)
	}
}

func TestVersionRotation(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, "versions", "current"), 0700)
	os.MkdirAll(filepath.Join(dataDir, "versions", "staging"), 0700)

	// Place marker files
	os.WriteFile(filepath.Join(dataDir, "versions", "current", "old"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(dataDir, "versions", "staging", "new"), []byte("new"), 0600)

	if err := rotateVersion(dataDir); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// current should have the new file
	if _, err := os.Stat(filepath.Join(dataDir, "versions", "current", "new")); err != nil {
		t.Error("expected 'new' in current after rotation")
	}

	// previous should have the old file
	if _, err := os.Stat(filepath.Join(dataDir, "versions", "previous", "old")); err != nil {
		t.Error("expected 'old' in previous after rotation")
	}
}

func TestRollback(t *testing.T) {
	dataDir := t.TempDir()
	os.MkdirAll(filepath.Join(dataDir, "versions", "current"), 0700)
	os.MkdirAll(filepath.Join(dataDir, "versions", "previous"), 0700)

	os.WriteFile(filepath.Join(dataDir, "versions", "current", "bad"), []byte("bad"), 0600)
	os.WriteFile(filepath.Join(dataDir, "versions", "previous", "good"), []byte("good"), 0600)

	if err := rollback(dataDir); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// current should have the good file
	if _, err := os.Stat(filepath.Join(dataDir, "versions", "current", "good")); err != nil {
		t.Error("expected 'good' in current after rollback")
	}

	// previous should have the bad file
	if _, err := os.Stat(filepath.Join(dataDir, "versions", "previous", "bad")); err != nil {
		t.Error("expected 'bad' in previous after rollback")
	}

	// rollback-tmp should not exist
	if _, err := os.Stat(filepath.Join(dataDir, "versions", "rollback-tmp")); !os.IsNotExist(err) {
		t.Error("rollback-tmp should be cleaned up")
	}
}

func TestInterruptedSwapRecovery(t *testing.T) {
	t.Run("rollback-tmp exists, current missing", func(t *testing.T) {
		dataDir := t.TempDir()
		os.MkdirAll(filepath.Join(dataDir, "versions", "rollback-tmp"), 0700)
		os.WriteFile(filepath.Join(dataDir, "versions", "rollback-tmp", "file"), []byte("data"), 0600)

		recoverInterruptedSwap(dataDir)

		if _, err := os.Stat(filepath.Join(dataDir, "versions", "current", "file")); err != nil {
			t.Error("expected rollback-tmp recovered to current")
		}
	})

	t.Run("rollback-tmp exists, current exists", func(t *testing.T) {
		dataDir := t.TempDir()
		os.MkdirAll(filepath.Join(dataDir, "versions", "rollback-tmp"), 0700)
		os.MkdirAll(filepath.Join(dataDir, "versions", "current"), 0700)

		recoverInterruptedSwap(dataDir)

		if _, err := os.Stat(filepath.Join(dataDir, "versions", "rollback-tmp")); !os.IsNotExist(err) {
			t.Error("expected rollback-tmp to be cleaned")
		}
	})
}

func TestIPCFiles(t *testing.T) {
	dataDir := t.TempDir()

	// Heartbeat
	if heartbeatExists(dataDir) {
		t.Error("heartbeat should not exist yet")
	}

	os.WriteFile(filepath.Join(dataDir, heartbeatFile), []byte(""), 0600)
	if !heartbeatExists(dataDir) {
		t.Error("heartbeat should exist now")
	}

	// Shutdown
	if shutdownRequested(dataDir) {
		t.Error("shutdown should not be requested yet")
	}

	writeShutdownRequested(dataDir)
	if !shutdownRequested(dataDir) {
		t.Error("shutdown should be requested now")
	}

	deleteShutdownFile(dataDir)
	if shutdownRequested(dataDir) {
		t.Error("shutdown should be cleared")
	}

	// Pending update
	if r := readPendingUpdate(dataDir); r != nil {
		t.Error("no pending update should exist")
	}

	writePendingUpdate(dataDir, &Release{Version: "2.0.0", URL: "https://example.com/v2"})
	r := readPendingUpdate(dataDir)
	if r == nil || r.Version != "2.0.0" {
		t.Errorf("expected pending update v2.0.0, got: %+v", r)
	}

	deletePendingUpdate(dataDir)
	if r := readPendingUpdate(dataDir); r != nil {
		t.Error("pending update should be deleted")
	}
}

func TestChecksumParsing(t *testing.T) {
	tests := []struct {
		input    string
		wantAlgo string
		wantHash string
		wantOK   bool
	}{
		{"sha256:abc123", "sha256", "abc123", true},
		{"", "", "", false},
		{"nocolon", "", "", false},
		{"sha256:", "", "", false},
	}

	for _, tt := range tests {
		algo, hash, ok := parseChecksum(tt.input)
		if algo != tt.wantAlgo || hash != tt.wantHash || ok != tt.wantOK {
			t.Errorf("parseChecksum(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, algo, hash, ok, tt.wantAlgo, tt.wantHash, tt.wantOK)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers for counter-based integration tests (no env var pollution)
// ---------------------------------------------------------------------------

// buildChildFromSource compiles a Go source string into a test binary.
// Returns the directory containing the binary and the binary filename.
func buildChildFromSource(t *testing.T, source string) (binDir, binName string) {
	t.Helper()
	binDir = t.TempDir()
	binName = "test-child"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	src := filepath.Join(binDir, "main.go")
	bin := filepath.Join(binDir, binName)

	if err := os.WriteFile(src, []byte(source), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test child: %v\n%s", err, out)
	}

	return binDir, binName
}

// installChild copies a child binary into a version directory (current, previous, etc).
func installChild(t *testing.T, binDir, binName, dataDir, versionDir string) {
	t.Helper()
	destDir := filepath.Join(dataDir, "versions", versionDir)
	if err := os.MkdirAll(destDir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", versionDir, err)
	}

	data, err := os.ReadFile(filepath.Join(binDir, binName))
	if err != nil {
		t.Fatalf("read child binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, binName), data, 0755); err != nil {
		t.Fatalf("write child to %s: %v", versionDir, err)
	}
}

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeFetcher serves a pre-built binary for update/bootstrap tests. Set
// downloadErr to simulate a permanently failing download instead (e.g. a
// firewall blocking the source) without needing a second fetcher type.
type fakeFetcher struct {
	binaryPath  string
	release     Release
	downloadErr error
}

func (f *fakeFetcher) LatestVersion(_ context.Context) (*Release, error) {
	return &f.release, nil
}

func (f *fakeFetcher) Download(_ context.Context, _ *Release, dst io.Writer, progress func(float64)) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	data, err := os.ReadFile(f.binaryPath)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(0.5)
	}
	_, err = dst.Write(data)
	if progress != nil {
		progress(1.0)
	}
	return err
}

// fakeUI records all UI method calls for assertion.
type fakeUI struct {
	mu              sync.Mutex
	splashMessages  []string
	hideSplashCount int
	errorMessages   []string
}

func (u *fakeUI) ShowSplash(status string) {
	u.mu.Lock()
	u.splashMessages = append(u.splashMessages, status)
	u.mu.Unlock()
}

func (u *fakeUI) UpdateProgress(float64, string) {}

func (u *fakeUI) HideSplash() {
	u.mu.Lock()
	u.hideSplashCount++
	u.mu.Unlock()
}

func (u *fakeUI) ShowError(msg string) {
	u.mu.Lock()
	u.errorMessages = append(u.errorMessages, msg)
	u.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Integration tests: real supervisor loop with real child processes
// ---------------------------------------------------------------------------

func TestRollbackOnCrashLoop(t *testing.T) {
	// Child crashes on first 3 invocations, then succeeds after rollback.
	// Uses a counter file in the state dir to track invocations.
	binDir, binName := buildChildFromSource(t, `package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	countFile := filepath.Join(stateDir, ".call_count")
	data, _ := os.ReadFile(countFile)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	count++
	os.WriteFile(countFile, []byte(strconv.Itoa(count)), 0600)

	if count <= 3 {
		os.Exit(1)
	}

	f, _ := os.Create(filepath.Join(stateDir, "heartbeat"))
	f.Close()
	os.WriteFile(filepath.Join(stateDir, "shutdown_requested"), []byte(""), 0600)
}
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")
	installChild(t, binDir, binName, dataDir, "previous")

	// Pre-populate state so the anti-oscillation guard allows rollback
	saveState(dataDir, &State{
		CurrentVersion:  "1.0.0",
		PreviousVersion: "0.9.0",
	})

	l := New(baseTestConfig(binName, dataDir))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit 0 after rollback recovery, got %d", code)
	}

	state, _ := loadState(dataDir)
	if state.RolledBackFrom == "" {
		t.Error("expected RolledBackFrom to be set after rollback")
	}
	if state.CurrentVersion != "0.9.0" {
		t.Errorf("expected current version 0.9.0 (rolled back), got %q", state.CurrentVersion)
	}
	if state.PreviousVersion != "1.0.0" {
		t.Errorf("expected previous version 1.0.0 (swapped), got %q", state.PreviousVersion)
	}
}

func TestUpdateFlow(t *testing.T) {
	// First invocation: child requests update (writes pending_update.json + shutdown).
	// After the launcher performs the update, second invocation: clean shutdown.
	binDir, binName := buildChildFromSource(t, `package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	countFile := filepath.Join(stateDir, ".call_count")
	data, _ := os.ReadFile(countFile)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	count++
	os.WriteFile(countFile, []byte(strconv.Itoa(count)), 0600)

	f, _ := os.Create(filepath.Join(stateDir, "heartbeat"))
	f.Close()

	if count == 1 {
		os.WriteFile(filepath.Join(stateDir, "pending_update.json"),
			[]byte("{\"version\":\"2.0.0\",\"url\":\"https://example.com/v2\",\"checksum\":\"\"}"), 0600)
	}

	os.WriteFile(filepath.Join(stateDir, "shutdown_requested"), []byte(""), 0600)
}
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")

	saveState(dataDir, &State{CurrentVersion: "1.0.0"})

	fetcher := &fakeFetcher{
		binaryPath: filepath.Join(binDir, binName),
		release:    Release{Version: "2.0.0", URL: "https://example.com/v2"},
	}

	cfg := baseTestConfig(binName, dataDir)
	cfg.Fetcher = fetcher
	l := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	state, _ := loadState(dataDir)
	if state.CurrentVersion != "2.0.0" {
		t.Errorf("expected current version 2.0.0, got %q", state.CurrentVersion)
	}
	if state.PreviousVersion != "1.0.0" {
		t.Errorf("expected previous version 1.0.0, got %q", state.PreviousVersion)
	}
	if state.ProbationUntil.IsZero() {
		t.Error("expected probation to be set after update")
	}

	// Verify version directories were rotated
	if !hasCurrentVersion(dataDir) {
		t.Error("expected current/ to exist after update")
	}
	if !hasPreviousVersion(dataDir) {
		t.Error("expected previous/ to exist after update")
	}
}

func TestUpdateFailureBackoffAndCooldown(t *testing.T) {
	// Child requests the same update on every single invocation (not just
	// the first) — modeling a persistently-unreachable download source: the
	// old version keeps starting, keeps seeing "a newer version exists",
	// and keeps asking to update, forever. Without failure tracking, this
	// spins the launcher in a tight, unbounded restart loop.
	binDir, binName := buildChildFromSource(t, `package main

import "os"

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	f, _ := os.Create(stateDir + "/heartbeat")
	f.Close()

	os.WriteFile(stateDir+"/pending_update.json",
		[]byte("{\"version\":\"2.0.0\",\"url\":\"https://example.com/v2\",\"checksum\":\"\"}"), 0600)
	os.WriteFile(stateDir+"/shutdown_requested", []byte(""), 0600)
}
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")
	saveState(dataDir, &State{CurrentVersion: "1.0.0"})

	cfg := baseTestConfig(binName, dataDir)
	cfg.Backoff = []time.Duration{5 * time.Millisecond, 10 * time.Millisecond}
	cfg.UpdateFailureThreshold = 3
	cfg.UpdateFailureWindow = 10 * time.Second
	cfg.UpdateFailureCooldown = 10 * time.Second // long enough to stay active for the rest of this test
	cfg.Fetcher = &fakeFetcher{
		release:     Release{Version: "2.0.0", URL: "https://example.com/v2"},
		downloadErr: errors.New("simulated permanent download failure"),
	}
	l := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit 0 on context cancellation, got %d", code)
	}

	state, err := loadState(dataDir)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if state.CurrentVersion != "1.0.0" {
		t.Errorf("expected current version to remain 1.0.0 (never a successful update), got %q", state.CurrentVersion)
	}
	if state.UpdateFailureCount != cfg.UpdateFailureThreshold {
		t.Errorf("expected update failure count to cap at threshold %d once cooldown engages, got %d",
			cfg.UpdateFailureThreshold, state.UpdateFailureCount)
	}
	if state.UpdateCooldownUntil.IsZero() || !state.UpdateCooldownUntil.After(time.Now()) {
		t.Errorf("expected an active cooldown after reaching the failure threshold, got %v", state.UpdateCooldownUntil)
	}
}

func TestBootstrapDownload(t *testing.T) {
	// "empty current dir leftover" regression: a previously interrupted install
	// can leave versions/current/ as an empty directory, and os.Rename refuses
	// to overwrite it ("move staging to current: ... file exists").
	binDir, binName := buildChildFromSource(t, `package main

import "os"

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	f, _ := os.Create(stateDir + "/heartbeat")
	f.Close()
	os.WriteFile(stateDir + "/shutdown_requested", []byte(""), 0600)
}
`)

	for _, tc := range []struct {
		name              string
		preCreateEmptyCur bool
	}{
		{"clean versions dir", false},
		{"empty current dir leftover", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()

			if tc.preCreateEmptyCur {
				if err := os.MkdirAll(currentVersionDir(dataDir), 0700); err != nil {
					t.Fatalf("setup: create empty current dir: %v", err)
				}
			}

			fetcher := &fakeFetcher{
				binaryPath: filepath.Join(binDir, binName),
				release:    Release{Version: "1.0.0", URL: "https://example.com/v1"},
			}

			cfg := baseTestConfig(binName, dataDir)
			cfg.Fetcher = fetcher
			l := New(cfg)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if code := l.Run(ctx); code != 0 {
				t.Fatalf("expected exit 0 after bootstrap, got %d", code)
			}

			if !hasCurrentVersion(dataDir) {
				t.Error("expected current/ to have child after bootstrap")
			}
		})
	}
}

func TestUICallbacks(t *testing.T) {
	// Child touches heartbeat, waits for the launcher to detect it, then shuts down.
	binDir, binName := buildChildFromSource(t, `package main

import (
	"os"
	"time"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	f, _ := os.Create(stateDir + "/heartbeat")
	f.Close()

	// Wait for heartbeat poll to fire and call HideSplash
	time.Sleep(1 * time.Second)

	os.WriteFile(stateDir + "/shutdown_requested", []byte(""), 0600)
}
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")

	ui := &fakeUI{}

	cfg := baseTestConfig(binName, dataDir)
	cfg.UI = ui
	l := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	ui.mu.Lock()
	defer ui.mu.Unlock()

	if len(ui.splashMessages) == 0 {
		t.Error("expected ShowSplash to be called at least once")
	}

	foundStarting := false
	for _, msg := range ui.splashMessages {
		if msg == "Starting test-app..." {
			foundStarting = true
			break
		}
	}
	if !foundStarting {
		t.Errorf("expected ShowSplash with 'Starting test-app...', got %v", ui.splashMessages)
	}

	if ui.hideSplashCount == 0 {
		t.Error("expected HideSplash to be called after heartbeat")
	}

	if len(ui.errorMessages) > 0 {
		t.Errorf("expected no errors, got %v", ui.errorMessages)
	}
}

func TestCrashLoopShowsError(t *testing.T) {
	// When crash loop has no rollback target, UI.ShowError should be called.
	binDir, binName := buildChildFromSource(t, `package main

import "os"

func main() { os.Exit(1) }
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")
	// No previous/ — no rollback possible

	ui := &fakeUI{}

	cfg := baseTestConfig(binName, dataDir)
	cfg.UI = ui
	l := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := l.Run(ctx)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}

	ui.mu.Lock()
	defer ui.mu.Unlock()

	if len(ui.errorMessages) == 0 {
		t.Error("expected ShowError to be called when crash loop has no rollback target")
	}
}

func TestContextCancellationTerminatesChild(t *testing.T) {
	// Child sleeps forever. Context cancellation should kill it.
	binDir, binName := buildChildFromSource(t, `package main

import (
	"os"
	"time"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	f, _ := os.Create(stateDir + "/heartbeat")
	f.Close()

	time.Sleep(1 * time.Hour)
}
`)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")

	l := New(baseTestConfig(binName, dataDir))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	l.Run(ctx)
	elapsed := time.Since(start)

	// Should finish within ~4 seconds (2s context + 2s kill timeout max)
	if elapsed > 10*time.Second {
		t.Errorf("expected fast termination, took %v", elapsed)
	}
}

func TestAfterLockAcquiredHookFires(t *testing.T) {
	cfg, _ := setupTestLauncher(t)

	hookCalled := false
	cfg.AfterLockAcquired = func(ctx context.Context) error {
		hookCalled = true
		return nil
	}

	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_EXIT_CODE", "0")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if code := l.Run(ctx); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !hookCalled {
		t.Error("AfterLockAcquired hook did not fire")
	}
}

func TestAfterLockAcquiredHookErrorAborts(t *testing.T) {
	cfg, _ := setupTestLauncher(t)

	cfg.AfterLockAcquired = func(ctx context.Context) error {
		return errors.New("hook failed")
	}

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if code := l.Run(ctx); code != 1 {
		t.Errorf("expected exit code 1 from failing hook, got %d", code)
	}
}

func TestResolveRelaunchArgs(t *testing.T) {
	args := []string{"--debug", "-update", "/old", "/new", "1234"}

	if got := resolveRelaunchArgs(nil, args); !reflect.DeepEqual(got, args) {
		t.Errorf("nil transform: got %v, want %v", got, args)
	}

	drop := func([]string) []string { return nil }
	if got := resolveRelaunchArgs(drop, args); len(got) != 0 {
		t.Errorf("drop transform: got %v, want empty", got)
	}

	keepFirst := func(a []string) []string { return a[:1] }
	if got := resolveRelaunchArgs(keepFirst, args); !reflect.DeepEqual(got, []string{"--debug"}) {
		t.Errorf("keepFirst transform: got %v, want [--debug]", got)
	}
}

func TestRelocateDestName(t *testing.T) {
	cases := []struct {
		name, binaryName, execPath, want string
	}{
		{"empty falls back to exec basename", "", "/tmp/MyApp-1.0-installer.exe", "MyApp-1.0-installer.exe"},
		{"explicit name wins", "MyApp.exe", "/tmp/MyApp-1.0-installer.exe", "MyApp.exe"},
		{"path traversal stripped", "../evil.exe", "/tmp/installer", "evil.exe"},
		{"subdir stripped", "sub/dir/MyApp.exe", "/tmp/installer", "MyApp.exe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := relocateDestName(tc.binaryName, tc.execPath); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
