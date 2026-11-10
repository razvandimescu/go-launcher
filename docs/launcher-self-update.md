# Launcher self-update

This document is the design of the launcher's update of itself. The update of the child is in [architecture.md](architecture.md).

The feature arrives in four parts. Each part works on its own.

| Part | What it adds | State |
|---|---|---|
| 1. Version | The launcher knows its own version and writes `launcher_ready`. | merged with this document |
| 2. Check | The launcher finds a different launcher release, downloads it to `<exe>.new` and verifies it. | not merged |
| 3. Swap | The launcher installs the staged file, as this document describes. Plain binary. | not merged |
| 4. macOS `.app` | The same update for a launcher inside an `.app` bundle. | not merged |

In this document, `exe` is the launcher binary in `InstallDir`. The child is the app that the launcher starts and watches.

## Core rules

These two rules come before every other decision. No decision may trade one of them for something else.

1. **One supervisor, the helper, is responsible for the whole update.** It checks the new file, swaps the files, starts the new launcher, decides if the update stays or is undone, stops the process that must go, and cleans up. No other process decides anything about the update.
2. **At every moment, the machine has a usable launcher and a usable child.**
   - **A launcher is always running.** The old launcher runs until the new one is proven healthy. If the update fails in any way, the old launcher is still there and starts the child.
   - **The files are always in a good state.** If every process stops at any moment (a crash, a kill, a power failure), the next start finds a launcher at `exe` that can start, and that launcher starts the child.

## Actors

| Actor | What it is | Role |
|---|---|---|
| **Old launcher** | The launcher that runs now (v1) | Stages the new file, starts the helper, and keeps running as the safety net. |
| **Helper** | The staged file `exe.new` (v2), started in helper mode | The supervisor of rule 1. |
| **New launcher** | `exe` after the swap (v2), a normal start | Must prove that it is healthy. Decides nothing about the update. |
| **Child** | The app | No role. It is not running during the update. |

The helper is the new version's program. So its start is also the first check of the new file, and the update code comes with each release.

Files, all next to each other:

| File | Content |
|---|---|
| `exe` | The launcher |
| `exe.new` | A staged launcher release |
| `exe.old` | The old launcher, during an update |
| `exe.bad` | A new launcher that failed |

## Successful update

```
 OLD LAUNCHER (v1)            FILES                          HELPER (v2)                    NEW LAUNCHER (v2)
 holds the lock               exe = v1
 stages, syncs ─────────────► exe.new = v2
 starts the helper ──────────────────────────────────────────► starts: the new file runs
 keeps running                launcher.lock = helper ◄────────┤ takes the lock over
                                                              │ version == announced
                              exe.old = v1 (second name) ◄────┤ link
                              exe = v2, exe.new gone ◄────────┤ rename, one step
                                                              │ starts exe ───────────────────► starts
                              launcher.lock = new ◄───────────┼────────────────────────────────┤ takes the lock over
                              launcher_ready = v2 ◄───────────┼────────────────────────────────┤ writes
                                                              │ sees it: healthy                │ waits for the helper
 ✕ stopped ◄──────────────────────────────────────────────────┤ stops the old launcher
                              exe.old gone ◄──────────────────┤ deletes
                                                              ✕ exits 0                         │ starts the child
```

The update runs at launcher start, before the child starts.

## What each actor does

### Old launcher

After it holds the lock, has loaded its state and has written `launcher_ready`:

```
 exe.old found                    → warn: "found exe.old: the previous launcher update
                                    did not finish its last step (cleanup + clean exit)"
 delete exe.old, exe.bad
 ask the fetcher for the latest launcher
   same version as mine                           → up to date (comparison A)
   version recorded as failed                     → skip
   different version                              → download to exe.new, verify sha256,
                                                    force to disk
 start the helper: exe.new, detached, same args,
   GO_LAUNCHER_REPLACE_PID=<my PID>
   GO_LAUNCHER_REPLACE_VERSION=<announced version>
 wait until the helper process ends, at most 6 × timeout. Keep running.
   (if the update succeeds, the helper stops this process before it ends)
   the helper did not end in time                 → stop the helper
 the helper ended and I am still running → the update did not succeed:
   take the lock again        refused → a new launcher runs: exit 0
   exe.old exists             → put the old file back (same step as the helper's ROLLBACK)
   delete exe.bad
   load the state again; record the failed version if the helper did not
   continue as today: start the child
```

### Helper

A launcher runs as the helper when `GO_LAUNCHER_REPLACE_PID` holds the PID of its parent. The check is at the top of `Run`.

```
 no relocation, no login item, no AfterLockAcquired hook; log to launcher.log
 read my own path once
 take the lock over from my parent, the old launcher          refused → exit 1
 from here: an error or a panic → ROLLBACK; the hard time limit (5 × timeout) → exit 1
 my version != announced version                              → record failed, exit 1   (comparison B)
 SWAP, exe is never missing:
   give the old file a second name:   link exe → exe.old
   replace exe in one step:           rename exe.new → exe      (retry until timeout)
 start exe (detached, same args, both variables removed)       → the new launcher
 wait for launcher_ready = (my version, that PID)              (timeout)
   healthy:
     stop the old launcher, wait until it is gone
     delete exe.old
     exit 0
   not healthy (timeout, or that process is gone) → ROLLBACK

 ROLLBACK
   stop the new launcher, wait until it is gone
   exe.old exists → give the new file a second name:  link exe → exe.bad
                    replace exe in one step:          rename exe.old → exe   (retry until timeout)
   record failed_launcher_version = announced version
   log "new version couldn't start, version upgrade aborted", with the reason
   exit 1                                             → the old launcher continues
```

### New launcher

A normal start of `exe`. Its parent, the helper, holds the lock.

```
 take the lock: the PID in launcher.lock is my parent → take it over
 load state, write launcher_ready (version, PID)
 no cleanup and no update check in this run
 wait until my parent, the helper, is gone        (2 × timeout)
 continue as today: start the child
```

A launcher whose parent does not hold the lock takes none of these steps. It starts as today.

## Why the rules hold

### The files and the lock after each step

| After this step | `exe` | `exe.old` | `exe.new` | Lock owner |
|---|---|---|---|---|
| Staged | old | absent | new | old launcher |
| Helper started | old | absent | new | helper |
| Link | old | old (same file) | new | helper |
| Rename | new | old | absent | helper |
| New launcher ready | new | old | absent | new launcher |
| Old launcher stopped, `exe.old` deleted | new | absent | absent | new launcher |

`exe` is present in every row. The lock has an owner in every row, so a second start during the update is refused.

### When something fails

| What goes wrong | Who handles it | Result |
|---|---|---|
| The helper cannot be started | old launcher | Records the failed version, starts the child. |
| The helper has another version than announced | helper | No file changes. Records the failed version, exits. The old launcher starts the child. |
| The link or the rename of SWAP fails | helper | ROLLBACK. The old launcher starts the child. |
| The new launcher cannot start, crashes, or hangs | helper | ROLLBACK. The old launcher starts the child. |
| The helper panics | helper | ROLLBACK from a deferred handler. |
| The helper hangs | helper | It exits at its hard time limit. The old launcher puts the old file back and starts the child. |
| The helper crashes or is killed | old launcher | If a healthy new launcher runs, the old launcher exits. If not, it puts the old file back and starts the child. |
| The system has frozen the helper | old launcher | Stops the helper after 6 × timeout, puts the old file back and starts the child. |

In every row, a launcher runs and starts the child. The failed version is not tried again until a different one is released.

### When every process stops at once

| Moment | `exe` at the next boot | What the next start does |
|---|---|---|
| Before the rename of SWAP | old launcher | Starts as today. Deletes `exe.old` if the link was made, with the warning. Tries the update again. |
| After the rename, before the new launcher is accepted | new launcher. It has run once as the helper. | Starts. Warns about `exe.old`, deletes it. |
| After the acceptance, before the cleanup | new launcher, accepted | Starts. Warns about `exe.old`, deletes it. |
| After the rename of ROLLBACK | old launcher | Starts as today. Deletes `exe.bad`. |

`exe` is a complete file in every row: the staged file is forced to disk before the swap, and each rename is one step.

### Who stops a process, who deletes a file

| Action | Who | When |
|---|---|---|
| Stop the old launcher | helper | After the new launcher is healthy |
| Stop the new launcher | helper | When it is not healthy |
| Stop the helper | old launcher | Only when the helper has not ended after 6 × timeout |
| Delete `exe.old` | helper | After the old launcher is gone |
| Delete `exe.bad` | old launcher | After a failed update, when the helper is gone |
| Delete leftovers of an update that did not finish | any launcher from a normal start | At start, after it has the lock, with the warning for `exe.old` |

The rows that are not the helper's have these causes:

- On Windows, a process cannot delete the file it runs from. After a ROLLBACK, the helper runs from `exe.bad`.
- A helper that has died cannot clean up.
- A helper that the system has frozen cannot end by itself. Without the stop, the child would never start.

## The two version comparisons

Both ignore a leading `v`.

| | Question | Who compares | Compared values | Update goes on if |
|---|---|---|---|---|
| A | Is there an update? | old launcher | announced version, its own `Config.Version` | different |
| B | Is the staged file the announced one? | helper | announced version, its own `Config.Version` | equal |

The announced version is the fetcher's answer to `LatestLauncher()`.

Without B, a release whose file reports another version than the fetcher announces would be installed. At the next start it would run comparison A with the same two values, find a difference and update again, at every start. Take `Config.Version` and the fetcher's version from one source, for example the release tag.

## Waits and limits

`T` is `Config.LauncherUpdateTimeout`, 30 s by default.

| Who waits | For what | Limit |
|---|---|---|
| helper | each rename retry | T |
| helper | `launcher_ready` | T |
| helper | a stopped process to be gone | T |
| helper | its whole run | 5 × T, then it exits |
| new launcher | the helper to be gone | 2 × T, then it continues |
| old launcher | the helper to end | 6 × T, then it stops the helper |

The new launcher waits longer than the helper's check, so the helper always decides first. The child then starts once, from one launcher.

A failed update delays the child by up to T in the common cases. That happens once for each failed version.

## Decisions

- **The old launcher keeps running during the update.** A launcher that is known to work is alive until the new one is proven healthy.
- **The helper is the staged file `exe.new`.** Its start proves that the new file runs on this machine before any file changes. A faulty helper costs one failed update, because the old launcher is alive behind it.
- **The swap is a second name plus one replacing rename.** No file is copied, and `exe` is never missing. Windows refuses to replace a running file that has one name, and allows it when the file has a second name.
- **No hard links, no update.** If the link fails, for example on a file system without hard links, the helper changes nothing and records the version as failed.
- **The staged file is forced to disk before the swap.** After a power failure, a file that was only in memory can be incomplete.
- **Each rename is repeated until it succeeds or the timeout passes.** On Windows, the system or a virus scanner can hold a file for a moment. `exe` stays in place during the retries.
- **Healthy means `launcher_ready` with the helper's version and the PID of the process that the helper started.** It proves that the new launcher started, took the lock and loaded its state.
- **The helper stops the old launcher.** The old launcher does not exit by itself. It has no child and no lock at that moment, so nothing is lost.
- **The new launcher has no exit logic for the update.** If it cannot write `launcher_ready`, it continues as today, and the helper stops it after the timeout. Its only duty is to wait for the helper before it starts the child. Otherwise a rollback could start a second child next to the first.
- **The lock passes along the chain: old launcher → helper → new launcher.** A launcher may take the lock when the PID in the lock file is its parent's. A launcher from a double-click or a login has another parent and is refused. A launcher removes only its own lock at exit.
- **Helper mode starts from an environment variable, not from a flag.** The app's `main()` runs before `Run`. An app with strict flag parsing would exit on an unknown flag. The variable is honoured only when it names the process's parent, so a stale or inherited value does nothing.
- **The old launcher is the safety net.** When the helper ends and the old launcher still runs, it takes the lock again and puts the old file back if `exe.old` exists.
- **`exe.old` at a normal start is evidence** that an update did not finish. The launcher logs the warning and deletes the file, so a stale `exe.old` can never be put back later.
- **A launcher that took the lock over runs no cleanup and no update check.** The cleanup would delete `exe.old` while the helper may still need it. One start can cause one update at most.
- **Every failed update records the version as failed.** `failed_launcher_version` in `launcher.json` is cleared when a different version is staged, so a fixed release can install.
- **Differs, not newer.** An older version from the fetcher means the newer one was withdrawn, so going back is correct. The app must never return a version older than the first self-updating one.
- **A failed check or update never blocks the child.**

## Facts this design depends on

Tested on Windows 11 (build 26200):

| Fact | Result |
|---|---|
| A running program's file can be renamed, by another program and by itself | ok |
| A running program's file cannot be deleted | refused |
| A running file with one name cannot be replaced | refused |
| A running file can get a second name (hard link) | ok |
| `exe` can be replaced in one step while the old file runs and has a second name | ok |
| The same in reverse while both processes run (ROLLBACK) | ok |
| A file can be deleted when its process is gone | ok |

Tested on macOS: a program renamed its own running file and restored it. Linux follows the same rules as macOS.

Not tested: a virus scanner that holds a file during a rename.
