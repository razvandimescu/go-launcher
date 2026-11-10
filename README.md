# go-launcher

[![Go Reference](https://pkg.go.dev/badge/github.com/razvandimescu/go-launcher.svg)](https://pkg.go.dev/github.com/razvandimescu/go-launcher)
[![CI](https://github.com/razvandimescu/go-launcher/actions/workflows/ci.yml/badge.svg)](https://github.com/razvandimescu/go-launcher/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/razvandimescu/go-launcher)](https://goreportcard.com/report/github.com/razvandimescu/go-launcher)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

The [Squirrel.Windows](https://github.com/Squirrel/Squirrel.Windows) pattern for Go. External supervisor with versioned directories, crash-based rollback, and zero-dependency child integration.

**[Website](https://razvandimescu.github.io/go-launcher/)** | **[Go Docs](https://pkg.go.dev/github.com/razvandimescu/go-launcher)**

<p align="center">
  <img src="splash-demo.gif" alt="Native macOS splash screen with animated spinner, progress bar, and status text" width="340">
  <br>
  <sub>Built-in native splash screen (macOS Cocoa/AppKit) -- no competing Go update library offers this.</sub>
</p>

## The Problem

Every Go auto-update library uses the same approach: **self-surgery** -- the running binary replaces itself on disk, then restarts. If the new version crashes at startup, recovery logic never executes. If the replacement is interrupted (power loss, OOM kill), the binary is corrupted with no rollback path.

Discord, Slack, and VS Code solved this years ago: a thin launcher manages versioned directories side-by-side. The old version stays intact until the new one proves stable.

We could not find a Go library that implements this pattern. **go-launcher** does.

## What It Does

go-launcher is a library you embed in a small launcher binary that supervises your actual application:

```
your-launcher          (thin binary, ~40 lines of your code + go-launcher)
  └── your-app         (your actual application, spawned as a child process)
```

The launcher handles:

- **Crash detection + automatic rollback** -- if the new version crash-loops, the previous version comes back automatically
- **Versioned directories** -- `versions/current/` and `versions/previous/` side-by-side
- **Update orchestration** -- download to staging, verify SHA-256 checksum, atomic rotation
- **Probation period** -- new versions must survive a configurable window before being marked stable
- **Process supervision** -- spawn, monitor, restart with configurable backoff
- **Anti-oscillation** -- prevents infinite swapping between two broken versions
- **Bootstrap download** -- if no child binary exists, download the latest version on first launch
- **Self-relocation** -- launcher copies itself from Downloads to a permanent install location on first run
- **Singleton enforcement** -- OS file lock prevents duplicate instances and is released when the launcher dies

Single dependency (`golang.org/x/sys`). The `child` package imported by your application has **zero transitive dependencies** -- standard library only.

## Quick Start

> For a runnable end-to-end demo, see the [`_example/`](./_example/) directory.

### Launcher side (your thin launcher binary)

```go
package main

import (
    "context"
    "os"

    "github.com/razvandimescu/go-launcher"
    "github.com/razvandimescu/go-launcher/fetch"
    "github.com/razvandimescu/go-launcher/ui/splash"
)

func main() {
    l := launcher.New(launcher.Config{
        AppName:         "My App",
        ChildBinaryName: "my-app",
        DataDir:         launcher.DefaultDataDir("MyApp"),
        InstallDir:      launcher.DefaultInstallDir("MyApp"),
        EnvVarName:      "MYAPP_LAUNCHER_STATE_DIR",
        Fetcher:         fetch.GitHubRelease("myorg", "myapp", fetch.AssetPattern("my-app-*")),
        UI:              splash.New(splash.Config{AppName: "My App"}),
    })

    os.Exit(l.Run(context.Background()))
}
```

### Child side (your actual application)

Version discovery is your application's concern -- poll your own API, check GitHub, read a config file. The child tells the launcher what to download:

```go
package main

import (
    "os"

    "github.com/razvandimescu/go-launcher/child"
)

func init() {
    child.SetEnvVar("MYAPP_LAUNCHER_STATE_DIR")
}

func main() {
    // ... application init ...

    // Signal healthy startup
    if child.IsManaged() {
        child.TouchHeartbeat()
    }

    // ... application runs ...

    // When you detect a new version is available:
    if child.IsManaged() {
        child.RequestUpdate("1.2.0", "https://example.com/my-app-1.2.0", "sha256:abc123...")
        os.Exit(0) // launcher handles download, rotation, and restart
    }
}
```

## How Existing Libraries Compare

| Library | Approach | Rollback | Supervisor | Versioned dirs | Built-in UI | Windows |
|---|---|---|---|---|---|---|
| [creativeprojects/go-selfupdate](https://github.com/creativeprojects/go-selfupdate) | Self-surgery | Apply-time only | No | No | No | Yes |
| [minio/selfupdate](https://github.com/minio/selfupdate) | Self-surgery | No | No | No | No | Yes |
| [sanbornm/go-selfupdate](https://github.com/sanbornm/go-selfupdate) | Self-surgery | No | No | No | No | Yes |
| [rhysd/go-github-selfupdate](https://github.com/rhysd/go-github-selfupdate) | Self-surgery | Apply-time only | No | No | No | Yes |
| [jpillora/overseer](https://github.com/jpillora/overseer) | Master/child | No | Yes | No | No | No |
| [fynelabs/selfupdate](https://github.com/fynelabs/selfupdate) | Self-surgery | Apply-time only | No | No | No | Yes |
| **go-launcher** | **External supervisor** | **Crash-based** | **Yes** | **Yes** | **Yes** | **Yes** |

**Apply-time rollback** means the `.old` file is restored if the rename/copy fails during the swap. It does not help if the new version starts successfully but crashes 30 seconds later.

**Crash-based rollback** means the launcher detects that the new version is crash-looping and automatically reverts to the previous known-good version -- even if the new version ran briefly before crashing.

> This table compares deployment architecture. Some of these libraries have strengths in other dimensions -- multi-backend support (GitHub/GitLab/S3), code signing verification, GOOS/GOARCH detection -- see each library's documentation for full feature sets.

## Architecture

```
$DATA_DIR/
  launcher.json                       # persistent state (7 flat JSON fields)
  launcher.lock                       # OS file lock (holds the PID)
  heartbeat                           # touched by child after healthy init
  pending_update.json                 # written by child when update is available
  shutdown_requested                  # flag file for clean exit
  versions/
    current/                          # active version (opaque directory)
    previous/                         # rollback target
    staging/                          # download in progress
```

Communication uses file-based IPC -- no sockets, no named pipes. The launcher sets an environment variable pointing to the data directory. The child writes files to signal state changes:

| Direction | Signal | Mechanism |
|---|---|---|
| Launcher → Child | "You are managed" | Environment variable |
| Child → Launcher | "I'm healthy" | Touch `heartbeat` file |
| Child → Launcher | "Update available" | Write `pending_update.json` + exit 0 |
| Child → Launcher | "Shut down" | Write `shutdown_requested` + exit 0 |

The launcher always restarts the child unless `shutdown_requested` exists with exit code 0. An unexpected exit 0 (without the file) is treated as a crash -- this avoids ambiguity from stray `os.Exit(0)` calls.

For the full supervisor loop, update flow, and rollback mechanics, see [docs/architecture.md](docs/architecture.md).

## Interfaces

go-launcher is interface-driven. Provide your own implementations or use the built-in ones.

### Fetcher (required for updates/bootstrap)

```go
type Fetcher interface {
    LatestVersion(ctx context.Context) (*Release, error)
    Download(ctx context.Context, release *Release, dst io.Writer, progress func(float64)) error
}
```

Built-in: `fetch.GitHubRelease()`, `fetch.HTTP()`.

### UI (optional)

```go
type UI interface {
    ShowSplash(status string)
    UpdateProgress(percent float64, status string)
    HideSplash()
    ShowError(msg string)
}
```

Built-in: [`ui/splash`](ui/splash/) provides native splash screens for macOS (Cocoa/AppKit) and Windows (GDI+) with animated spinner, progress bar, and configurable branding:

```go
import "github.com/razvandimescu/go-launcher/ui/splash"

UI: splash.New(splash.Config{
    AppName:   "My App",
    Logo:      logoBytes,   // PNG, or nil for text-only
    AccentHex: "#2E67B2",   // spinner + progress bar color
})
```

Returns a silent no-op on Linux or when CGo is unavailable on macOS. Pass `nil` for fully headless operation.

### Registrar (optional)

```go
type Registrar interface {
    RegisterLoginItem(binaryPath string) error
    UnregisterLoginItem() error
    RegisterService(binaryPath string, args []string) error
    UnregisterService() error
}
```

Handles OS-level registration (login items, system services). No built-in implementations yet — provide your own or pass `nil` to skip.

## Configuration

```go
launcher.Config{
    // Required
    AppName         string          // display name
    ChildBinaryName string          // binary filename in versions/current/
    DataDir         string          // state, versions, IPC files
    InstallDir      string          // where the launcher lives permanently
    EnvVarName      string          // env var set on child process

    // Stable filename in InstallDir after self-relocation. Empty = use the
    // basename of the binary that was actually launched (which may be
    // version-tagged by an installer, e.g. MyApp-1.0-installer.exe).
    LauncherBinaryName string

    // Optional (sensible defaults)
    ChildArgs         []string        // args forwarded to child (default: none)
    Backoff           []time.Duration // restart delays (default: [2s, 5s, 15s])
    CrashThreshold    int             // crashes before rollback (default: 3)
    CrashWindow       time.Duration   // crash count resets after this (default: 5min)
    ProbationDuration time.Duration   // new version probation (default: 10min)
    KillTimeout       time.Duration   // SIGTERM -> SIGKILL escalation (default: 30s)

    // Pluggable (all optional except Fetcher if you want updates)
    UI        UI          // nil = headless
    Fetcher   Fetcher     // nil = no bootstrap/updates
    Registrar Registrar   // nil = skip OS registration

    // Lifecycle hooks
    AfterLockAcquired func(ctx context.Context) error
        // Runs once the launcher has won the singleton lockfile race and
        // before any bootstrap/supervisor work. Use it for one-time setup
        // that must only execute on the actively supervising process —
        // legacy cleanup, migration tasks, registry changes that would
        // race a concurrent launcher invocation. A non-nil error from the
        // hook causes Run to return 1.

    RelaunchArgs func(args []string) []string
        // Transforms the arguments forwarded to the relocated launcher
        // copy after self-relocation. Default forwards os.Args[1:]
        // verbatim. Return nil or an empty slice to drop all arguments —
        // useful when the launcher was invoked through a legacy installer
        // protocol whose arguments should not propagate.
}
```

| Platform | DefaultDataDir | DefaultInstallDir |
|---|---|---|
| macOS | `~/Library/Application Support/{appName}/` | `/Applications/` |
| Windows | `%LOCALAPPDATA%\{appName}\` | `%LOCALAPPDATA%\{appName}\` |
| Linux | `~/.local/share/{appName}/` | `~/.local/bin/` |

go-launcher logs via `log/slog`. Configure `slog.SetDefault()` before calling `Run()`.

## Security

go-launcher downloads binaries from the internet and executes them. The built-in fetchers enforce HTTPS. Downloaded artifacts are verified against SHA-256 checksums provided in the `Release.Checksum` field.

Code signing verification is not currently built in. If your threat model requires it, implement a custom `Fetcher` that verifies signatures before writing to the `dst` writer.

## Limitations

- **Single-unit child.** The child must be a single binary or a directory managed as an opaque unit.
- **No self-update.** The launcher does not update itself. This is deliberate -- the launcher is a thin, stable binary that changes rarely. Update it via your installer or a manual download.
- **Full downloads only.** No delta/incremental updates. For most Go binaries (5-30MB), full downloads complete in seconds.
- **No download resumption.** Interrupted downloads restart from the beginning.

## License

MIT

## Contributing

Issues and pull requests are welcome. See the [_example/](./_example/) directory for a working launcher + child pair you can use for testing.

