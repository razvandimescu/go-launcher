package launcher

import (
	"context"
	"testing"
	"time"
)

// crashThenSucceedChild crashes on its first 3 invocations, then heartbeats and
// requests a clean shutdown. Invocations are counted via a file in the state
// dir, shared across spawns and across a rollback (current/ and previous/ are
// the same binary in these tests).
const crashThenSucceedChild = `package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	stateDir := os.Getenv("TEST_LAUNCHER_STATE_DIR")
	if stateDir == "" { os.Exit(1) }

	countFile := filepath.Join(stateDir, ".call_count")
	data, _ := os.ReadFile(countFile)
	count, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	count++
	os.WriteFile(countFile, []byte(strconv.Itoa(count)), 0600)

	if count <= 3 {
		os.Exit(1)
	}

	f, _ := os.Create(filepath.Join(stateDir, "heartbeat"))
	f.Close()
	os.WriteFile(filepath.Join(stateDir, "shutdown_requested"), []byte(""), 0600)
}
`

// TestRollbackRecoversWithEmptyVersions is the reported bug: a manually seeded
// or bootstrap-only install has empty version strings, so the anti-oscillation
// guard ("" != "") used to skip a perfectly good previous/ and exit forever.
// With the fix, the launcher rolls back and recovers.
func TestRollbackRecoversWithEmptyVersions(t *testing.T) {
	binDir, binName := buildChildFromSource(t, crashThenSucceedChild)

	dataDir := t.TempDir()
	installChild(t, binDir, binName, dataDir, "current")
	installChild(t, binDir, binName, dataDir, "previous")

	// No state file at all: CurrentVersion and PreviousVersion are both "".
	l := New(baseTestConfig(binName, dataDir))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if code := l.Run(ctx); code != 0 {
		t.Fatalf("expected exit 0 after rollback recovery, got %d", code)
	}
	if !hasCurrentVersion(dataDir) {
		t.Error("expected current/ to still hold a binary after recovery")
	}
}
