package launcher

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	currentDir     = "current"
	previousDir    = "previous"
	stagingDir     = "staging"
	rollbackTmpDir = "rollback-tmp"
)

func versionsDir(dataDir string) string {
	return filepath.Join(dataDir, "versions")
}

func currentVersionDir(dataDir string) string {
	return filepath.Join(versionsDir(dataDir), currentDir)
}

func previousVersionDir(dataDir string) string {
	return filepath.Join(versionsDir(dataDir), previousDir)
}

func stagingVersionDir(dataDir string) string {
	return filepath.Join(versionsDir(dataDir), stagingDir)
}

func rollbackTmpVersionDir(dataDir string) string {
	return filepath.Join(versionsDir(dataDir), rollbackTmpDir)
}

// childBinaryPath returns the full path to the child binary inside versions/current/.
func childBinaryPath(dataDir, binaryName string) string {
	return filepath.Join(currentVersionDir(dataDir), binaryName)
}

// hasEntries reports whether dir exists and is non-empty.
func hasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// rotateVersion moves staging/ → current/ (with current/ → previous/).
// Caller must ensure the child process is fully dead before calling.
func rotateVersion(dataDir string) error {
	cur := currentVersionDir(dataDir)
	prev := previousVersionDir(dataDir)
	stg := stagingVersionDir(dataDir)

	// Verify staging exists
	if _, err := os.Stat(stg); err != nil {
		return fmt.Errorf("staging dir does not exist: %w", err)
	}

	// Remove old previous if it exists
	if err := os.RemoveAll(prev); err != nil {
		return fmt.Errorf("remove previous: %w", err)
	}

	// Move current → previous (if current exists)
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, prev); err != nil {
			return fmt.Errorf("move current to previous: %w", err)
		}
	}

	// Move staging → current
	if err := os.Rename(stg, cur); err != nil {
		return fmt.Errorf("move staging to current: %w", err)
	}

	return nil
}

// rollback swaps current/ and previous/ using a temp directory for atomicity.
// Caller must ensure the child process is fully dead before calling.
func rollback(dataDir string) error {
	cur := currentVersionDir(dataDir)
	prev := previousVersionDir(dataDir)
	tmp := rollbackTmpVersionDir(dataDir)

	// Step 1: current → rollback-tmp
	if err := os.Rename(cur, tmp); err != nil {
		return fmt.Errorf("move current to rollback-tmp: %w", err)
	}

	// Step 2: previous → current
	if err := os.Rename(prev, cur); err != nil {
		// Try to recover: rollback-tmp → current
		if rerr := os.Rename(tmp, cur); rerr != nil {
			slog.Error("CRITICAL: failed to recover from interrupted rollback",
				"rename_error", err, "recovery_error", rerr)
		}
		return fmt.Errorf("move previous to current: %w", err)
	}

	// Step 3: rollback-tmp → previous
	if err := os.Rename(tmp, prev); err != nil {
		// Non-critical: current and previous are correct, tmp is a leftover
		slog.Warn("failed to move rollback-tmp to previous (will be cleaned on next start)",
			"error", err)
	}

	return nil
}

// recoverInterruptedSwap handles the case where rollback-tmp/ exists from
// a previously interrupted rollback.
func recoverInterruptedSwap(dataDir string) {
	tmp := rollbackTmpVersionDir(dataDir)
	cur := currentVersionDir(dataDir)

	if _, err := os.Stat(tmp); os.IsNotExist(err) {
		return // no interrupted swap
	}

	if _, err := os.Stat(cur); os.IsNotExist(err) {
		// current is missing — rollback-tmp is the only version we have
		slog.Warn("recovering from interrupted swap: rollback-tmp → current")
		if err := os.Rename(tmp, cur); err != nil {
			slog.Error("failed to recover rollback-tmp to current", "error", err)
		}
	} else {
		// current exists — rollback-tmp is a stale leftover
		slog.Info("cleaning stale rollback-tmp directory")
		os.RemoveAll(tmp)
	}
}

// cleanStagingDir removes the staging directory if it exists (interrupted download).
func cleanStagingDir(dataDir string) {
	stg := stagingVersionDir(dataDir)
	if _, err := os.Stat(stg); err == nil {
		slog.Info("cleaning interrupted staging directory")
		os.RemoveAll(stg)
	}
}

// ensureVersionDirs creates the versions/ directory structure.
func ensureVersionDirs(dataDir string) error {
	return os.MkdirAll(versionsDir(dataDir), 0700)
}
