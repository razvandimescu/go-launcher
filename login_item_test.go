package launcher

import (
	"context"
	"testing"
	"time"
)

type countingRegistrar struct{ loginItems int }

func (r *countingRegistrar) RegisterLoginItem(string) error { r.loginItems++; return nil }
func (r *countingRegistrar) UnregisterLoginItem() error     { return nil }

func TestOnlyTheLockHolderRegistersTheLoginItem(t *testing.T) {
	cfg, dataDir := setupTestLauncher(t)
	reg := &countingRegistrar{}
	cfg.Registrar = reg
	t.Setenv("CHILD_HEARTBEAT", "1")
	t.Setenv("CHILD_SHUTDOWN", "1")
	t.Setenv("CHILD_EXIT_CODE", "0")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	running := newLockfile(dataDir)
	if err := running.Acquire(); err != nil {
		t.Fatal(err)
	}
	if code := New(cfg).Run(ctx); code != 1 {
		t.Fatalf("expected exit code 1 while another launcher holds the lock, got %d", code)
	}
	if reg.loginItems != 0 {
		t.Errorf("a launcher that lost the lock registered itself as the login item")
	}

	running.Release()
	if code := New(cfg).Run(ctx); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if reg.loginItems != 1 {
		t.Errorf("expected the lock holder to register the login item once, got %d", reg.loginItems)
	}
}
