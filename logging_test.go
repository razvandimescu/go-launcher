package launcher

import (
	"bytes"
	"errors"
	"io"
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

// A failing writer wrapped in bestEffortWriter must not abort io.MultiWriter,
// even when it sits first in the chain — the windowsgui-stderr failure mode
// that left launcher.log empty on GUI-subsystem builds.
func TestBestEffortWriterDoesNotAbortMultiWriter(t *testing.T) {
	var buf bytes.Buffer
	w := io.MultiWriter(bestEffortWriter{failingWriter{}}, &buf)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("MultiWriter aborted on wrapped failing writer: %v", err)
	}
	if n != 5 || buf.String() != "hello" {
		t.Fatalf("downstream writer got %q (n=%d), want %q", buf.String(), n, "hello")
	}
}

func TestSetupFileLoggingTeesToFile(t *testing.T) {
	prev := slog.Default()
	defer slog.SetDefault(prev)

	path := filepath.Join(t.TempDir(), "launcher.log")
	closeLog := setupFileLogging(path)
	defer closeLog()

	slog.Info("marker line for test")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "marker line for test") {
		t.Errorf("log file does not contain the logged line, got %q", got)
	}
}
