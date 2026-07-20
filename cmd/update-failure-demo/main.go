// update-failure-demo exercises the update-failure backoff/cooldown path
// against a real, unreachable download source — the manual counterpart to
// TestUpdateFailureBackoffAndCooldown, which uses a fake fetcher that fails
// instantly. This one drives the real fetch.HTTP code path with real network
// failure semantics, so it is worth running on each supported OS.
//
// The binary is both launcher and child: the launcher installs a copy of
// itself as the managed child, and that child requests the same unreachable
// update on every start — exactly the scenario that used to spin a tight,
// zero-backoff restart loop.
//
//	go run ./cmd/update-failure-demo -reset
//	go run ./cmd/update-failure-demo -url https://10.255.255.1/app.zip  # black-hole (times out)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	launcher "github.com/razvandimescu/go-launcher"
	"github.com/razvandimescu/go-launcher/child"
	"github.com/razvandimescu/go-launcher/fetch"
)

const (
	envVar      = "UPDATE_FAILURE_DEMO_STATE_DIR"
	urlEnvVar   = "UPDATE_FAILURE_DEMO_URL"
	nextVersion = "2.0.0"
)

func childBinaryName() string {
	if runtime.GOOS == "windows" {
		return "demo-child.exe"
	}
	return "demo-child"
}

func main() {
	child.SetEnvVar(envVar)
	if child.IsManaged() {
		runChild()
		return
	}
	runLauncher()
}

// runChild models an app that always believes a newer version exists and asks
// for it on every start, so the failing download is retried indefinitely.
func runChild() {
	if err := child.TouchHeartbeat(); err != nil {
		fmt.Fprintln(os.Stderr, "child: heartbeat failed:", err)
	}
	fmt.Printf("  child pid=%d running, will request %s\n", os.Getpid(), nextVersion)
	time.Sleep(750 * time.Millisecond) // look healthy before asking to update

	if err := child.RequestUpdate(nextVersion, os.Getenv(urlEnvVar), ""); err != nil {
		fmt.Fprintln(os.Stderr, "child: RequestUpdate failed:", err)
		os.Exit(1)
	}
}

func runLauncher() {
	dataDir := flag.String("data-dir", filepath.Join(os.TempDir(), "go-launcher-update-demo"), "launcher data directory")
	url := flag.String("url", "http://127.0.0.1:1/app.zip", "download URL that will fail (a black-holed host such as https://10.255.255.1/app.zip fails by timeout instead)")
	threshold := flag.Int("threshold", 3, "failed downloads before cooldown")
	window := flag.Duration("window", 2*time.Minute, "window for counting consecutive failures")
	cooldown := flag.Duration("cooldown", 30*time.Second, "pause on further attempts once threshold is reached")
	timeout := flag.Duration("timeout", 90*time.Second, "stop the demo after this long")
	reset := flag.Bool("reset", false, "wipe the data directory before running")
	flag.Parse()

	if *reset {
		if err := os.RemoveAll(*dataDir); err != nil {
			fmt.Fprintln(os.Stderr, "reset failed:", err)
			os.Exit(1)
		}
	}
	if err := installSelfAsChild(*dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "failed to install demo child:", err)
		os.Exit(1)
	}
	os.Setenv(urlEnvVar, *url)

	fmt.Printf("data dir: %s\nlog:      %s\nstate:    %s\nsource:   %s (expected to fail)\n\n",
		*dataDir,
		filepath.Join(*dataDir, "launcher.log"),
		filepath.Join(*dataDir, "launcher.json"),
		*url)
	fmt.Printf("expect: %d failed downloads, then \"pausing update attempts\" and a %s cooldown\n\n",
		*threshold, *cooldown)

	l := launcher.New(launcher.Config{
		AppName:                "Update Failure Demo",
		ChildBinaryName:        childBinaryName(),
		DataDir:                *dataDir,
		EnvVarName:             envVar,
		Backoff:                []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
		UpdateFailureThreshold: *threshold,
		UpdateFailureWindow:    *window,
		UpdateFailureCooldown:  *cooldown,
		Fetcher:                fetch.HTTP(*url, fetch.WithHTTPClient(&http.Client{Timeout: 5 * time.Second})),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	code := l.Run(ctx)

	s := l.Status()
	fmt.Printf("\n--- result ---\ncurrent_version:      %q (unchanged = no bad update was installed)\nupdate_failure_count: %d\nupdate_cooldown_until: %s\n",
		s.CurrentVersion, s.UpdateFailureCount, cooldownState(s.UpdateCooldownUntil))
	os.Exit(code)
}

func cooldownState(until time.Time) string {
	if until.IsZero() {
		return "not set (threshold never reached)"
	}
	if remaining := time.Until(until); remaining > 0 {
		return fmt.Sprintf("%s (active, %s remaining)", until.Format(time.RFC3339), remaining.Truncate(time.Second))
	}
	return until.Format(time.RFC3339) + " (expired)"
}

// installSelfAsChild copies this binary into versions/current/ so the
// supervisor has something to run without a bootstrap download.
func installSelfAsChild(dataDir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dst := filepath.Join(dataDir, "versions", "current", childBinaryName())
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
