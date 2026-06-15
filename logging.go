package launcher

import (
	"io"
	"log/slog"
	"os"
)

const logFileMaxBytes = 1 << 20 // rotate to <path>.1 once it exceeds 1 MiB

// setupFileLogging tees the default slog logger to stderr and a log file. The
// launcher runs without a console in GUI/login-item mode, where stderr is
// discarded — a file is the only record of bootstrap/update failures. The file
// is rotated once at startup if it has grown past logFileMaxBytes, bounding
// on-disk size to roughly 2×. Returns a closer; safe with an empty path.
func setupFileLogging(path string) func() {
	if path == "" {
		return func() {}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > logFileMaxBytes {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("launcher log file unavailable, continuing with stderr only",
			"path", path, "error", err)
		return func() {}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), nil)))
	return func() { f.Close() }
}
