package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRestoreOldLauncher(t *testing.T) {
	for _, tc := range []struct {
		name    string
		swapped bool
	}{
		{"after the swap, the new file is kept as exe.bad", true},
		{"after the link only, exe.old is dropped", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "launcher")
			os.WriteFile(exe, []byte("v1"), 0755)
			if err := os.Link(exe, exe+".old"); err != nil {
				t.Fatal(err)
			}
			if tc.swapped {
				os.WriteFile(exe+".new", []byte("v2"), 0755)
				if err := os.Rename(exe+".new", exe); err != nil {
					t.Fatal(err)
				}
			}

			if err := restoreOldLauncher(exe, time.Second); err != nil {
				t.Fatalf("restore: %v", err)
			}
			assertContent(t, exe, "v1")
			assertMissing(t, exe+".old")
			if tc.swapped {
				assertContent(t, exe+".bad", "v2")
			} else {
				assertMissing(t, exe+".bad")
			}
		})
	}
}

// selfUpdateLauncherSource is a launcher whose version is set at build time.
// A build with broken set fails at every start that is not the update helper.
const selfUpdateLauncherSource = `package main

import (
	"context"
	"io"
	"os"
	"time"

	launcher "github.com/razvandimescu/go-launcher"
)

var version, broken string

type fetcher struct{}

func (fetcher) LatestVersion(context.Context) (*launcher.Release, error) { return &launcher.Release{}, nil }

func (fetcher) LatestLauncher(context.Context) (*launcher.Release, error) {
	return &launcher.Release{Version: os.Getenv("E2E_NEW_VERSION"), Checksum: os.Getenv("E2E_NEW_CHECKSUM")}, nil
}

func (fetcher) Download(_ context.Context, _ *launcher.Release, dst io.Writer, _ func(float64)) error {
	f, err := os.Open(os.Getenv("E2E_NEW_LAUNCHER"))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

func main() {
	if broken != "" && os.Getenv("GO_LAUNCHER_REPLACE_PID") == "" {
		os.Exit(3)
	}
	os.Exit(launcher.New(launcher.Config{
		AppName:               "e2e",
		ChildBinaryName:       os.Getenv("E2E_CHILD"),
		DataDir:               os.Getenv("E2E_DATA"),
		EnvVarName:            "TEST_LAUNCHER_STATE_DIR",
		Version:               version,
		Fetcher:               fetcher{},
		HeartbeatPoll:         50 * time.Millisecond,
		LauncherUpdateTimeout: 5 * time.Second,
	}).Run(context.Background()))
}
`

// buildSelfUpdateLauncher builds selfUpdateLauncherSource against this module.
func buildSelfUpdateLauncher(t *testing.T, out, version, broken string) {
	t.Helper()
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	goMod := "module e2e\n\ngo 1.26\n\nrequire github.com/razvandimescu/go-launcher v0.0.0\n\nreplace github.com/razvandimescu/go-launcher => " + repo + "\n"
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "go.mod"), []byte(goMod), 0644)
	os.WriteFile(filepath.Join(src, "go.sum"), sum, 0644)
	os.WriteFile(filepath.Join(src, "main.go"), []byte(selfUpdateLauncherSource), 0644)

	cmd := exec.Command("go", "build", "-mod=mod", "-o", out,
		"-ldflags", "-X main.version="+version+" -X main.broken="+broken, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build launcher %s: %v\n%s", version, err, b)
	}
}

func TestLauncherSelfUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds launcher binaries")
	}
	childDir, childName := buildChildFromSource(t, envVarChildSource)
	exeName := "launcher"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}

	for _, tc := range []struct {
		name        string
		broken      string
		wantVersion string
		wantFailed  string
	}{
		{"healthy new launcher replaces the old one", "", "2.0.0", ""},
		{"failed new launcher is rolled back", "1", "1.0.0", "2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			installChild(t, childDir, childName, dataDir, "current")
			exe := filepath.Join(t.TempDir(), exeName)
			newLauncher := filepath.Join(t.TempDir(), exeName)
			buildSelfUpdateLauncher(t, exe, "1.0.0", "")
			buildSelfUpdateLauncher(t, newLauncher, "2.0.0", tc.broken)
			oldBinary, _ := os.ReadFile(exe)
			newBinary, _ := os.ReadFile(newLauncher)
			sum := sha256.Sum256(newBinary)

			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(),
				"E2E_DATA="+dataDir, "E2E_CHILD="+childName, "E2E_NEW_LAUNCHER="+newLauncher,
				"E2E_NEW_VERSION=2.0.0", "E2E_NEW_CHECKSUM=sha256:"+hex.EncodeToString(sum[:]),
				"CHILD_HEARTBEAT=1", "CHILD_SHUTDOWN=1", "CHILD_SLEEP=500ms")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			oldDone := make(chan struct{})
			go func() { cmd.Wait(); close(oldDone) }()

			// The run ends when the launcher that started the child has seen it shut down.
			finished := func() bool {
				select {
				case <-oldDone:
				default:
					return false
				}
				_, err := os.Stat(filepath.Join(dataDir, lockFileName))
				return os.IsNotExist(err)
			}
			if !waitFor(60*time.Second, finished) {
				log, _ := os.ReadFile(filepath.Join(dataDir, "launcher.log"))
				t.Fatalf("update did not finish\n%s", log)
			}

			want := oldBinary
			if tc.wantVersion == "2.0.0" {
				want = newBinary
			}
			if got, _ := os.ReadFile(exe); string(got) != string(want) {
				log, _ := os.ReadFile(filepath.Join(dataDir, "launcher.log"))
				t.Fatalf("exe is not version %s\n%s", tc.wantVersion, log)
			}
			for _, leftover := range []string{".new", ".old", ".bad"} {
				assertMissing(t, exe+leftover)
			}
			state, _ := loadState(dataDir)
			if state.FailedLauncherVersion != tc.wantFailed {
				t.Errorf("failed launcher version = %q, want %q", state.FailedLauncherVersion, tc.wantFailed)
			}
			if ready := readLauncherReady(dataDir); ready == nil || ready.Version != tc.wantVersion {
				t.Errorf("launcher_ready = %+v, want version %s", ready, tc.wantVersion)
			}
		})
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Errorf("%s = %q (err %v), want %q", filepath.Base(path), got, err, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s should not exist (err %v)", filepath.Base(path), err)
	}
}
