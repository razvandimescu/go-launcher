package launcher

import (
	"context"
	"testing"
	"time"
)

func TestHungStartIsKilledAndCountedAsCrash(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)
	cfg.HeartbeatTimeout = 200 * time.Millisecond

	// Never touches the heartbeat and never exits on its own.
	t.Setenv("CHILD_SLEEP", "1h")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// No previous version to roll back to, so three kills end the loop.
	if code := New(cfg).Run(ctx); code != 1 {
		t.Fatalf("expected exit code 1 after three hung starts, got %d", code)
	}
	if state, _ := loadState(dataDir); state.CrashCount < cfg.CrashThreshold {
		t.Errorf("expected crash count >= %d, got %d", cfg.CrashThreshold, state.CrashCount)
	}
}

func TestHealthyChildOutlivesHeartbeatTimeout(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)
	// Long enough for a freshly built binary's slow first launch on macOS.
	cfg.HeartbeatTimeout = 2 * time.Second

	const childRuntime = 3 * time.Second
	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_SLEEP", childRuntime.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	if code := New(cfg).Run(ctx); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if ran := time.Since(start); ran < childRuntime {
		t.Errorf("healthy child was killed after %v, before its %v run ended", ran, childRuntime)
	}
	if state, _ := loadState(dataDir); state.CrashCount != 0 {
		t.Errorf("expected no crash recorded, got %d", state.CrashCount)
	}
}
