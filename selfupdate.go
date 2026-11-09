package launcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// launcherCheckTimeout bounds the LatestLauncher call, so a slow endpoint
// cannot hold back the child's start.
const launcherCheckTimeout = 30 * time.Second

// checkLauncherUpdate stages a launcher release that differs from
// cfg.Version at <executable>.new. Every failure is logged and ignored: the
// child starts as it would without the check.
func (l *Launcher) checkLauncherUpdate(ctx context.Context) {
	lf, ok := l.cfg.Fetcher.(LauncherFetcher)
	if !ok || l.cfg.Version == "" {
		return
	}
	execPath, err := os.Executable()
	if err == nil {
		execPath, err = filepath.EvalSymlinks(execPath)
	}
	if err != nil {
		slog.Warn("launcher update check skipped: executable path unknown", "error", err)
		return
	}
	// Replacing the binary inside a bundle would break its code signature.
	if strings.Contains(execPath, ".app/") {
		slog.Info("launcher update check skipped inside an .app bundle", "path", execPath)
		return
	}
	stageLauncherUpdate(ctx, &l.cfg, lf, execPath+".new")
}

// stageLauncherUpdate downloads the latest launcher to dest when its version
// differs from cfg.Version, older included: an older answer means the newer
// release was withdrawn. It reports whether a release was staged.
func stageLauncherUpdate(ctx context.Context, cfg *Config, lf LauncherFetcher, dest string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, launcherCheckTimeout)
	release, err := lf.LatestLauncher(checkCtx)
	cancel()
	if err != nil {
		slog.Warn("launcher update check failed", "error", err)
		return false
	}
	if sameVersion(release.Version, cfg.Version) {
		slog.Info("launcher up to date", "version", cfg.Version)
		return false
	}
	// The launcher replaces itself, so unlike a child update it never runs unverified.
	if algo, _, ok := parseChecksum(release.Checksum); !ok || algo != "sha256" {
		slog.Warn("launcher update skipped: release has no sha256 checksum", "version", release.Version)
		return false
	}
	if cfg.UI != nil {
		cfg.UI.ShowSplash("Updating launcher to " + release.Version + "...")
	}
	if err := downloadVerified(ctx, cfg, release, dest); err != nil {
		slog.Warn("launcher update download failed", "version", release.Version, "error", err)
		return false
	}
	slog.Info("launcher update staged", "from", cfg.Version, "to", release.Version, "path", dest)
	return true
}

// sameVersion compares versions, ignoring a leading "v".
func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}
