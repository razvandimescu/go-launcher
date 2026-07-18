// Package child provides helpers for applications managed by go-launcher.
//
// This package has zero transitive dependencies beyond the standard library.
// Import it in your managed application to communicate with the launcher.
package child

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultEnvVar     = "LAUNCHER_STATE_DIR"
	heartbeatFile     = "heartbeat"
	shutdownFile      = "shutdown_requested"
	pendingUpdateFile = "pending_update.json"
)

var envVarOverride string

// SetEnvVar overrides the environment variable name used to detect the launcher.
// Call this before any other child functions if your launcher uses a custom env var.
func SetEnvVar(name string) {
	envVarOverride = name
}

func envVar() string {
	if envVarOverride != "" {
		return envVarOverride
	}
	return defaultEnvVar
}

// IsManaged returns true if this process was spawned by a go-launcher instance.
func IsManaged() bool {
	return StateDir() != ""
}

// StateDir returns the launcher's state directory, or empty string if not managed.
func StateDir() string {
	return os.Getenv(envVar())
}

// TouchHeartbeat signals to the launcher that the application has initialized
// successfully. Call this after your application is ready to serve.
func TouchHeartbeat() error {
	dir := StateDir()
	if dir == "" {
		return nil // not managed, no-op
	}

	path := filepath.Join(dir, heartbeatFile)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("touch heartbeat: %w", err)
	}
	return f.Close()
}

// RequestShutdown signals the launcher that this is a clean exit.
// The launcher will not restart the application.
// Call this before os.Exit(0).
func RequestShutdown() error {
	dir := StateDir()
	if dir == "" {
		return nil
	}

	path := filepath.Join(dir, shutdownFile)
	return os.WriteFile(path, []byte(""), 0600)
}

// RequestUpdate signals the launcher to download and install a new version,
// then restart the application. This writes both pending_update.json and
// shutdown_requested. Call this before os.Exit(0).
func RequestUpdate(version, url, checksum string) error {
	dir := StateDir()
	if dir == "" {
		return fmt.Errorf("not managed by launcher")
	}

	update := struct {
		Version  string `json:"version"`
		URL      string `json:"url"`
		Checksum string `json:"checksum"`
	}{
		Version:  version,
		URL:      url,
		Checksum: checksum,
	}

	data, err := json.MarshalIndent(update, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal update: %w", err)
	}

	// Atomic write: tmp + rename
	path := filepath.Join(dir, pendingUpdateFile)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write pending update: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename pending update: %w", err)
	}

	// Write shutdown_requested
	return RequestShutdown()
}
