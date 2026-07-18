package child

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsManaged(t *testing.T) {
	SetEnvVar("TEST_CHILD_STATE_DIR")

	os.Unsetenv("TEST_CHILD_STATE_DIR")
	if IsManaged() {
		t.Error("should not be managed without env var")
	}

	os.Setenv("TEST_CHILD_STATE_DIR", "/tmp/test")
	defer os.Unsetenv("TEST_CHILD_STATE_DIR")
	if !IsManaged() {
		t.Error("should be managed with env var set")
	}
}

func TestTouchHeartbeat(t *testing.T) {
	dir := t.TempDir()
	SetEnvVar("TEST_CHILD_STATE_DIR")
	os.Setenv("TEST_CHILD_STATE_DIR", dir)
	defer os.Unsetenv("TEST_CHILD_STATE_DIR")

	if err := TouchHeartbeat(); err != nil {
		t.Fatalf("touch heartbeat: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, heartbeatFile)); err != nil {
		t.Error("heartbeat file should exist")
	}
}

func TestRequestShutdown(t *testing.T) {
	dir := t.TempDir()
	SetEnvVar("TEST_CHILD_STATE_DIR")
	os.Setenv("TEST_CHILD_STATE_DIR", dir)
	defer os.Unsetenv("TEST_CHILD_STATE_DIR")

	if err := RequestShutdown(); err != nil {
		t.Fatalf("request shutdown: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, shutdownFile)); err != nil {
		t.Error("shutdown file should exist")
	}
}

func TestRequestUpdate(t *testing.T) {
	dir := t.TempDir()
	SetEnvVar("TEST_CHILD_STATE_DIR")
	os.Setenv("TEST_CHILD_STATE_DIR", dir)
	defer os.Unsetenv("TEST_CHILD_STATE_DIR")

	if err := RequestUpdate("2.0.0", "https://example.com/v2", "sha256:abc"); err != nil {
		t.Fatalf("request update: %v", err)
	}

	// Verify pending_update.json
	data, err := os.ReadFile(filepath.Join(dir, pendingUpdateFile))
	if err != nil {
		t.Fatal("pending update file should exist")
	}

	var update struct {
		Version  string `json:"version"`
		URL      string `json:"url"`
		Checksum string `json:"checksum"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if update.Version != "2.0.0" || update.URL != "https://example.com/v2" || update.Checksum != "sha256:abc" {
		t.Errorf("unexpected update: %+v", update)
	}

	// Verify shutdown_requested also exists
	if _, err := os.Stat(filepath.Join(dir, shutdownFile)); err != nil {
		t.Error("shutdown file should also exist after RequestUpdate")
	}
}

func TestNotManagedIsNoOp(t *testing.T) {
	SetEnvVar("TEST_CHILD_STATE_DIR")
	os.Unsetenv("TEST_CHILD_STATE_DIR")

	// These should all be no-ops, not errors
	if err := TouchHeartbeat(); err != nil {
		t.Errorf("touch heartbeat should be no-op: %v", err)
	}
	if err := RequestShutdown(); err != nil {
		t.Errorf("request shutdown should be no-op: %v", err)
	}
}
