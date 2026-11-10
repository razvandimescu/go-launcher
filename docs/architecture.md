# Architecture

This document covers the internal mechanics of go-launcher: the supervisor loop, update flow, and rollback protocol. For a high-level overview, see the [README](../README.md).

## Supervisor Loop

```
on startup:
  self-relocate if first run
  acquire lockfile
  recover from any interrupted operations
  if no child binary exists: bootstrap download via Fetcher

loop:
  if crash threshold reached:
    if rollback target exists: rollback, continue
    else: exit 1

  spawn child
  wait for exit:
    heartbeat touched     -> mark healthy, hide splash
    no heartbeat by HeartbeatTimeout (if set) -> kill, handled as a crash
    shutdown_requested    -> if update pending: download, rotate, continue
                            else: exit 0
    no shutdown_requested -> record crash, backoff, continue

  on exit: check if probation should be cleared
```

## Update Flow

```
child detects new version (your app's logic)
  -> child calls child.RequestUpdate(version, url, checksum)
  -> child exits 0
  -> launcher reads pending_update.json
  -> launcher downloads to versions/staging/
  -> launcher verifies SHA-256 checksum
  -> launcher rotates: current/ -> previous/, staging/ -> current/
  -> launcher sets probation period
  -> launcher spawns new version from current/
  -> if new version crash-loops -> automatic rollback to previous/
```

## Rollback Protocol

Three atomic renames with crash recovery:

```
current/      -> rollback-tmp/
previous/     -> current/
rollback-tmp/ -> previous/
```

If interrupted at any point (power loss, OOM kill), the launcher recovers to a consistent state on next startup by inspecting which directories exist:

- If `rollback-tmp/` exists but `current/` does not: rename `rollback-tmp/` to `current/`
- If `rollback-tmp/` and `current/` both exist: remove `rollback-tmp/` (rollback completed)
- If `staging/` exists: remove it (interrupted download)

## Probation

After an update, the new version enters a probation period (default: 10 minutes). Probation is checked at exit time, not via polling:

1. When the child exits, the launcher checks whether:
   - The heartbeat file was touched after spawn time
   - The child ran longer than the probation duration
2. If both conditions are met, crash counters are reset and the version is marked stable
3. If the child crashes during probation, it counts toward the crash threshold like any other crash

## Crash Window

Crashes are counted within a sliding window (default: 5 minutes). If the crash count reaches the threshold within the window, rollback is triggered. If enough time passes between crashes, the counter resets. This prevents a single transient failure from triggering rollback while still catching persistent crash loops.

## Anti-Oscillation

After rolling back from version B to version A, the `rolled_back_from` field records version B. If version A also crash-loops, the launcher will **not** roll back to B again -- it exits with code 1 instead. This prevents infinite oscillation between two broken versions.

## Design Decisions

**File-based IPC over sockets/pipes.** Files are debuggable (`cat heartbeat`), survive process crashes, and work identically across platforms. No protocol versioning, no serialization library, no connection management.

**Always restart by default.** The launcher restarts the child on any exit (including exit 0) unless `shutdown_requested` exists. This eliminates ambiguity from multiple `os.Exit(0)` call sites.

**Atomic state writes.** All persistent state uses write-to-tmp + rename. No truncated reads from interrupted writes.
