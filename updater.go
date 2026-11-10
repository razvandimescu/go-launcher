package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// bootstrapDownload downloads the latest child binary into versions/current/.
func bootstrapDownload(ctx context.Context, cfg *Config) error {
	if cfg.Fetcher == nil {
		return fmt.Errorf("no fetcher configured, cannot bootstrap")
	}

	release, err := cfg.Fetcher.LatestVersion(ctx)
	if err != nil {
		return fmt.Errorf("fetch latest version: %w", err)
	}

	slog.Info("bootstrap: downloading child binary", "version", release.Version, "url", release.URL)

	if err := downloadToStaging(ctx, cfg, release); err != nil {
		return err
	}

	// current/ may exist empty after an interrupted install — Rename won't overwrite it.
	cur := currentVersionDir(cfg.DataDir)
	stg := stagingVersionDir(cfg.DataDir)

	if err := ensureVersionDirs(cfg.DataDir); err != nil {
		return fmt.Errorf("create versions dir: %w", err)
	}

	if err := os.RemoveAll(cur); err != nil {
		return fmt.Errorf("remove stale current: %w", err)
	}

	if err := os.Rename(stg, cur); err != nil {
		return fmt.Errorf("move staging to current: %w", err)
	}

	return nil
}

// performUpdate downloads the release and rotates versions.
func performUpdate(ctx context.Context, cfg *Config, release *Release) error {
	slog.Info("updating child binary", "version", release.Version, "url", release.URL)

	if err := downloadToStaging(ctx, cfg, release); err != nil {
		return err
	}

	if err := rotateVersion(cfg.DataDir); err != nil {
		cleanStagingDir(cfg.DataDir)
		return fmt.Errorf("rotate version: %w", err)
	}

	return nil
}

// downloadToStaging downloads a release to the staging directory with checksum
// verification and progress reporting.
func downloadToStaging(ctx context.Context, cfg *Config, release *Release) error {
	stgDir := stagingVersionDir(cfg.DataDir)

	// Clean any previous staging
	os.RemoveAll(stgDir)
	if err := os.MkdirAll(stgDir, 0700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	if err := downloadVerified(ctx, cfg, release, filepath.Join(stgDir, cfg.ChildBinaryName)); err != nil {
		os.RemoveAll(stgDir)
		return err
	}
	return nil
}

// downloadVerified downloads a release to destPath, verifies its sha256 when
// one is given, and makes it executable. On error, destPath is removed.
func downloadVerified(ctx context.Context, cfg *Config, release *Release, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(destPath), err)
	}

	var w io.Writer = f
	var hasher *checksumWriter

	// Set up checksum verification if provided
	if algo, hash, ok := parseChecksum(release.Checksum); ok && algo == "sha256" {
		hasher = &checksumWriter{
			writer:   f,
			hasher:   sha256.New(),
			expected: hash,
		}
		w = hasher
	}

	progress := func(pct float64) {
		if cfg.UI != nil {
			status := fmt.Sprintf("Downloading %s...", release.Version)
			cfg.UI.UpdateProgress(pct, status)
		}
	}

	if err := cfg.Fetcher.Download(ctx, release, w, progress); err != nil {
		f.Close()
		os.Remove(destPath)
		return fmt.Errorf("download: %w", err)
	}

	// A file that is only in memory can be incomplete after a power failure.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(destPath)
		return fmt.Errorf("sync %s: %w", filepath.Base(destPath), err)
	}

	if err := f.Close(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("close %s: %w", filepath.Base(destPath), err)
	}

	// Verify checksum
	if hasher != nil {
		actual := hex.EncodeToString(hasher.hasher.Sum(nil))
		if actual != hasher.expected {
			os.Remove(destPath)
			return fmt.Errorf("checksum mismatch: expected %s, got %s", hasher.expected, actual)
		}
		slog.Info("checksum verified", "algo", "sha256")
	}

	// Make binary executable
	if err := os.Chmod(destPath, 0755); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("chmod %s: %w", filepath.Base(destPath), err)
	}

	return nil
}

type checksumWriter struct {
	writer   io.Writer
	hasher   hash.Hash
	expected string
}

func (cw *checksumWriter) Write(p []byte) (int, error) {
	n, err := cw.writer.Write(p)
	if n > 0 {
		cw.hasher.Write(p[:n])
	}
	return n, err
}

// parseChecksum splits "sha256:abc123" into ("sha256", "abc123", true).
func parseChecksum(s string) (algo, hash string, ok bool) {
	if s == "" {
		return "", "", false
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
