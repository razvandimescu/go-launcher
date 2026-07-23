package launcher

import (
	"io"
	"log/slog"
	"os"
)

const logFileMaxBytes = 1 << 20 // rotate to <path>.1 once it exceeds 1 MiB

// bestEffortWriter always reports success. In a console-less GUI process
// (windowsgui subsystem) os.Stderr is an invalid handle whose writes fail,
// and io.MultiWriter stops at the first writer error — an unusable stderr
// must never block the log-file write.
type bestEffortWriter struct{ w io.Writer }

// logStderr is swapped out in tests to prove a dead stderr cannot block the
// log-file write.
var logStderr io.Writer = os.Stderr

func (b bestEffortWriter) Write(p []byte) (int, error) {
	b.w.Write(p)
	return len(p), nil
}

// setupFileLogging tees the default slog logger to stderr and a log file. The
// launcher runs without a console in GUI/login-item mode, where stderr is
// discarded — a file is the only record of bootstrap/update failures. The file
// is rotated once at startup if it has grown past logFileMaxBytes, bounding
// on-disk size to roughly 2×. Returns a closer that restores the previous
// default logger; safe with an empty path.
func setupFileLogging(path string) func() {
	if path == "" {
		return func() {}
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > logFileMaxBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			slog.Warn("log rotation failed, file may grow unbounded", "path", path, "error", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("launcher log file unavailable, continuing with stderr only",
			"path", path, "error", err)
		return func() {}
	}
	// Stderr first so a file-write error (disk full, volume gone) cannot
	// silence console output; the wrapper keeps a dead stderr from blocking
	// the file write, so ordering costs nothing.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(bestEffortWriter{logStderr}, f), nil)))
	return func() {
		slog.SetDefault(prev)
		f.Close()
	}
}
