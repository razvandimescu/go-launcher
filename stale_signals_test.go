package launcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaleShutdownNoteDoesNotHideACrash(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)
	// Left by an earlier child whose exit no launcher saw.
	if err := writeShutdownRequested(dataDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHILD_EXIT_CODE", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if code := New(cfg).Run(ctx); code != 1 {
		t.Fatalf("expected exit code 1 after a crash loop, got %d", code)
	}
}

func TestStalePendingUpdateIsNotInstalled(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)
	fetcher := &fakeFetcher{binaryPath: childBinaryPath(dataDir, cfg.ChildBinaryName)}
	cfg.Fetcher = fetcher
	if err := writePendingUpdate(dataDir, &Release{Version: "9.9.9", URL: "https://example.com/stale"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_EXIT_CODE", "0")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if code := New(cfg).Run(ctx); code != 0 {
		t.Fatalf("expected exit code 0 on a clean shutdown, got %d", code)
	}
	if n := fetcher.downloads.Load(); n != 0 {
		t.Errorf("a clean shutdown installed a stale pending update (%d downloads)", n)
	}
}

// writePendingUpdate atomically writes pending_update.json.
func writePendingUpdate(dataDir string, r *Release) error {
	return atomicWriteJSON(filepath.Join(dataDir, pendingUpdateFile), r)
}

// writeShutdownRequested creates the shutdown_requested flag file.
func writeShutdownRequested(dataDir string) error {
	path := filepath.Join(dataDir, shutdownFile)
	return os.WriteFile(path, []byte(""), 0600)
}
