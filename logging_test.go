package launcher

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupFileLoggingRotatesOversizedFileAtStartup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher.log")

	if err := os.WriteFile(path, []byte(strings.Repeat("x", logFileMaxBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}

	closeLog := setupFileLogging(path)
	defer closeLog()

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected oversized log rotated to <path>.1: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected fresh log file: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("expected fresh log to start empty, got %d bytes", fi.Size())
	}
}

func TestSetupFileLoggingAppendsToSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "launcher.log")

	if err := os.WriteFile(path, []byte("seed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	closeLog := setupFileLogging(path)
	closeLog()

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("did not expect rotation for a small log file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "seed\n") {
		t.Errorf("expected existing content preserved, got %q", got)
	}
}

func TestSetupFileLoggingEmptyPathIsNoop(t *testing.T) {
	// Empty path must not panic and must return a usable closer.
	setupFileLogging("")()
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("the handle is invalid")
}

// The wrapper reports success even when the inner writer fails, so it can
// never abort an io.MultiWriter chain.
func TestBestEffortWriterReportsSuccessDespiteInnerFailure(t *testing.T) {
	n, err := bestEffortWriter{failingWriter{}}.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
	}
}

func TestSetupFileLoggingTeesToFile(t *testing.T) {
	prev := slog.Default()

	path := filepath.Join(t.TempDir(), "launcher.log")
	closeLog := setupFileLogging(path)
	slog.Info("marker line for test")
	closeLog()

	if slog.Default() != prev {
		t.Error("closer did not restore the previous default logger")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "marker line for test") {
		t.Errorf("log file does not contain the logged line, got %q", got)
	}
}

// Pins the wiring inside setupFileLogging: with the stderr slot dead (the
// windowsgui failure mode), log lines must still reach the file. Fails if
// the stderr writer is ever left unwrapped again.
func TestSetupFileLoggingSurvivesDeadStderr(t *testing.T) {
	prevStderr := logStderr
	logStderr = failingWriter{}
	defer func() { logStderr = prevStderr }()

	path := filepath.Join(t.TempDir(), "launcher.log")
	closeLog := setupFileLogging(path)
	defer closeLog()

	slog.Info("written despite dead stderr")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "written despite dead stderr") {
		t.Errorf("dead stderr blocked the file write, log file got %q", got)
	}
}
