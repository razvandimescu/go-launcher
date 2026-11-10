package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	heartbeatFile     = "heartbeat"
	shutdownFile      = "shutdown_requested"
	pendingUpdateFile = "pending_update.json"
)

// heartbeatExists returns true if the heartbeat file is present. The launcher
// deletes the heartbeat before every spawn, so existence proves the current
// child created it. mtime comparison is unreliable: Linux's current_time()
// uses ktime_get_coarse_real_ts64 (jiffy-quantized) while Go's time.Now()
// is nanosecond-precise, so a sub-jiffy spawn-then-touch sequence can leave
// mtime <= spawnTime even though the touch happened strictly later.
func heartbeatExists(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, heartbeatFile))
	return err == nil
}

// shutdownRequested returns true if the shutdown_requested file exists.
func shutdownRequested(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, shutdownFile))
	return err == nil
}

// deleteShutdownFile removes the shutdown_requested file.
func deleteShutdownFile(dataDir string) {
	os.Remove(filepath.Join(dataDir, shutdownFile))
}

// readPendingUpdate reads and parses pending_update.json. Returns nil if
// the file does not exist or is malformed.
func readPendingUpdate(dataDir string) *Release {
	data, err := os.ReadFile(filepath.Join(dataDir, pendingUpdateFile))
	if err != nil {
		return nil
	}

	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil
	}
	if r.URL == "" {
		return nil
	}
	return &r
}

// deletePendingUpdate removes pending_update.json and its tmp file.
func deletePendingUpdate(dataDir string) {
	os.Remove(filepath.Join(dataDir, pendingUpdateFile))
	os.Remove(filepath.Join(dataDir, pendingUpdateFile+".tmp"))
}

// deleteHeartbeat removes the heartbeat file.
func deleteHeartbeat(dataDir string) {
	os.Remove(filepath.Join(dataDir, heartbeatFile))
}
