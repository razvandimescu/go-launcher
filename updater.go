package launcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// bootstrapDownload downloads the latest child binary into versions/current/.
func bootstrapDownload(ctx context.Context, cfg *Config) error {
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
	want, ok := strings.CutPrefix(release.Checksum, "sha256:")
	if release.Checksum != "" && (!ok || want == "") {
		return fmt.Errorf("unsupported checksum %q, want sha256:<hex>", release.Checksum)
	}

	stgDir := stagingVersionDir(cfg.DataDir)

	// Clean any previous staging
	os.RemoveAll(stgDir)
	if err := os.MkdirAll(stgDir, 0700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	destPath := filepath.Join(stgDir, cfg.ChildBinaryName)
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create staging file: %w", err)
	}

	hasher := sha256.New()

	progress := func(pct float64) {
		if cfg.UI != nil {
			status := fmt.Sprintf("Downloading %s...", release.Version)
			cfg.UI.UpdateProgress(pct, status)
		}
	}

	if err := cfg.Fetcher.Download(ctx, release, io.MultiWriter(f, hasher), progress); err != nil {
		f.Close()
		os.RemoveAll(stgDir)
		return fmt.Errorf("download: %w", err)
	}

	if err := f.Close(); err != nil {
		os.RemoveAll(stgDir)
		return fmt.Errorf("close staging file: %w", err)
	}

	if want != "" {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, want) {
			os.RemoveAll(stgDir)
			return fmt.Errorf("checksum mismatch: expected %s, got %s", want, actual)
		}
		slog.Info("checksum verified", "algo", "sha256")
	}

	// Make binary executable
	if err := os.Chmod(destPath, 0755); err != nil {
		os.RemoveAll(stgDir)
		return fmt.Errorf("chmod staging binary: %w", err)
	}

	return nil
}
