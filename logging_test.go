package launcher

import (
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
