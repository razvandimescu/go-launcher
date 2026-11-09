package launcher

import (
	"context"
	"io"
)

// UI is an optional interface for displaying splash/progress during launcher
// operations. Pass nil for headless operation — all UI calls are nil-safe.
type UI interface {
	ShowSplash(status string)
	UpdateProgress(percent float64, status string)
	HideSplash()
	ShowError(msg string)
}

// Fetcher checks for and downloads child binaries. Required for bootstrap
// downloads and update orchestration. Pass nil if the child binary is
// pre-installed and updates are not managed by the launcher.
type Fetcher interface {
	LatestVersion(ctx context.Context) (*Release, error)
	Download(ctx context.Context, release *Release, dst io.Writer, progress func(float64)) error
}

// LauncherFetcher is an optional Fetcher extension that enables launcher
// self-update. The launcher release is downloaded with Fetcher.Download.
type LauncherFetcher interface {
	LatestLauncher(ctx context.Context) (*Release, error)
}

// Registrar handles OS-level registration (login items, system services).
// Pass nil to skip registration.
type Registrar interface {
	RegisterLoginItem(binaryPath string) error
	UnregisterLoginItem() error
	RegisterService(binaryPath string, args []string) error
	UnregisterService() error
}

// Release describes a downloadable version of the child binary.
type Release struct {
	Version  string `json:"version"`
	URL      string `json:"url"`
	Checksum string `json:"checksum"` // "sha256:abc123..." or empty to skip verification
}
