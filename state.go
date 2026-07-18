package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State represents the persistent launcher state, stored as a flat JSON file.
type State struct {
	CurrentVersion   string    `json:"current_version"`
	PreviousVersion  string    `json:"previous_version"`
	ProbationUntil   time.Time `json:"probation_until"`
	CrashCount       int       `json:"crash_count"`
	CrashWindowStart time.Time `json:"crash_window_start"`
	LastHealthyAt    time.Time `json:"last_healthy_at"`
	RolledBackFrom   string    `json:"rolled_back_from"`

	// Update-failure tracking is deliberately separate from crash tracking:
	// a failed update download never touches CurrentVersion, so there's
	// nothing to roll back from, and it shouldn't feed the crash/rollback
	// counters (a persistently unreachable download source isn't a bad
	// version of the app). Instead, repeated failures earn a cooldown that
	// pauses further update attempts for a while — see recordUpdateFailure.
	UpdateFailureCount       int       `json:"update_failure_count"`
	UpdateFailureWindowStart time.Time `json:"update_failure_window_start"`
	UpdateCooldownUntil      time.Time `json:"update_cooldown_until"`
}

// resetCrashState clears crash tracking and probation fields.
func (s *State) resetCrashState() {
	s.CrashCount = 0
	s.CrashWindowStart = time.Time{}
	s.ProbationUntil = time.Time{}
	s.RolledBackFrom = ""
}

// resetUpdateFailureState clears update-failure tracking and any active
// cooldown — called after an update attempt actually succeeds.
func (s *State) resetUpdateFailureState() {
	s.UpdateFailureCount = 0
	s.UpdateFailureWindowStart = time.Time{}
	s.UpdateCooldownUntil = time.Time{}
}

const stateFileName = "launcher.json"

func stateFilePath(dataDir string) string {
	return filepath.Join(dataDir, stateFileName)
}

func loadState(dataDir string) (*State, error) {
	path := stateFilePath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupted state — reset to defaults
		return &State{}, nil
	}
	return &s, nil
}

func saveState(dataDir string, s *State) error {
	return atomicWriteJSON(stateFilePath(dataDir), s)
}

// atomicWriteJSON marshals v to JSON and writes it atomically via tmp+rename.
func atomicWriteJSON(path string, v any) error {
	tmpPath := path + ".tmp"

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(tmpPath), err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", filepath.Base(path), err)
	}
	return nil
}

// deleteStateTmpFiles removes stale .tmp files left by interrupted writes.
func deleteStateTmpFiles(dataDir string) {
	os.Remove(stateFilePath(dataDir) + ".tmp")
	os.Remove(filepath.Join(dataDir, "pending_update.json.tmp"))
}
