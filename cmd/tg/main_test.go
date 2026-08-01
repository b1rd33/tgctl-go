package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type legacyState struct {
	files map[string][]byte
	infos map[string]os.FileInfo
}

func buildTGProcess(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tg")
	cmd := exec.Command("go", "build", "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tg: %v\n%s", err, out)
	}
	return path
}

func buildTGProcessWithVersion(t *testing.T, version string) string {
	return buildTGProcessWithBuildInfo(t, version, "", "release")
}

func buildTGProcessWithBuildInfo(t *testing.T, version, commit, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tg")
	const linkerPrefix = "github.com/b1rd33/tgctl-go/internal/commands."
	ldflags := strings.Join([]string{
		"-X " + linkerPrefix + "Version=" + version,
		"-X " + linkerPrefix + "Commit=" + commit,
		"-X " + linkerPrefix + "VersionSource=" + source,
	}, " ")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", ldflags, "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tg with version %q: %v\n%s", version, err, out)
	}
	return path
}

func seedLegacyState(t *testing.T, root string) legacyState {
	t.Helper()
	files := map[string][]byte{
		"telegram.sqlite": []byte("legacy-db"),
		"tg.session":      []byte("legacy-session"),
		"audit.log":       []byte("legacy-audit"),
		"tg.session.lock": []byte("legacy-lock"),
		"media/photo.jpg": []byte("legacy-media"),
	}
	infos := map[string]os.FileInfo{}
	wantTime := time.Unix(1_700_000_000, 0)
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, wantTime, wantTime); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		infos[name] = info
	}
	return legacyState{files: files, infos: infos}
}

func runTGProcess(t *testing.T, binary, root string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tg %v: %v\n%s", args, err, out)
	}
}

func runTGProcessResult(t *testing.T, binary, root string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("tg %v: %v\n%s", args, err, out)
	}
	return string(out), exitErr.ExitCode()
}

func runTGProcessStreams(t *testing.T, binary, root string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("tg %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

func TestProcessMalformedArgumentsUseOneDispatchedFailureWithoutArtifacts(t *testing.T) {
	binary := buildTGProcess(t)
	env := withoutStartupSafetyEnv(os.Environ())
	tests := []struct {
		name              string
		command           string
		args              []string
		modeBeforeBadFlag bool
	}{
		{name: "get message id", command: "get-msg", args: []string{"get-msg", "1", "nope"}},
		{name: "edit message id", command: "edit-msg", args: []string{"edit-msg", "1", "nope", "text"}},
		{name: "pin message id", command: "pin-msg", args: []string{"pin-msg", "1", "nope"}},
		{name: "unpin message id", command: "unpin-msg", args: []string{"unpin-msg", "1", "nope"}},
		{name: "reaction message id", command: "react", args: []string{"react", "1", "nope", "thumbs-up"}},
		{name: "forward message ids", command: "forward", args: []string{"forward", "1", "2", "nope"}},
		{name: "get message id with trailing junk", command: "get-msg", args: []string{"get-msg", "1", "2x"}},
		{name: "get message id with separated junk", command: "get-msg", args: []string{"get-msg", "1", "2 x"}},
		{name: "get zero message id", command: "get-msg", args: []string{"get-msg", "1", "0"}},
		{name: "get negative message id", command: "get-msg", args: []string{"get-msg", "1", "--", "-2"}, modeBeforeBadFlag: true},
		{name: "get overflowing message id", command: "get-msg", args: []string{"get-msg", "1", "9223372036854775808"}},
		{name: "edit message id with trailing junk", command: "edit-msg", args: []string{"edit-msg", "1", "2x", "text"}},
		{name: "pin message id with trailing junk", command: "pin-msg", args: []string{"pin-msg", "1", "2x"}},
		{name: "unpin message id with trailing junk", command: "unpin-msg", args: []string{"unpin-msg", "1", "2x"}},
		{name: "reaction message id with trailing junk", command: "react", args: []string{"react", "1", "2x", "thumbs-up"}},
		{name: "forward CSV trailing junk", command: "forward", args: []string{"forward", "1", "2", "1,2x"}},
		{name: "forward CSV empty segment", command: "forward", args: []string{"forward", "1", "2", "1,,2"}},
		{name: "forward CSV whitespace", command: "forward", args: []string{"forward", "1", "2", "1, 2"}},
		{name: "forward CSV zero", command: "forward", args: []string{"forward", "1", "2", "1,0"}},
		{name: "forward CSV negative", command: "forward", args: []string{"forward", "1", "2", "1,-2"}},
		{name: "forward CSV overflow", command: "forward", args: []string{"forward", "1", "2", "1,9223372036854775808"}},
		{name: "folder id with trailing junk", command: "folder-show", args: []string{"folder-show", "2x"}},
		{name: "topic id with trailing junk", command: "topic-edit", args: []string{"topic-edit", "1", "2x"}},
		{name: "session hash with trailing junk", command: "terminate-session", args: []string{"terminate-session", "2x"}},
		{name: "empty send text", command: "send", args: []string{"send", "1", ""}},
		{name: "wrong arity", command: "get-msg", args: []string{"get-msg", "1"}},
		{name: "malformed flag", command: "download-media", args: []string{"download-media", "1", "2", "--max-size-mb=nope"}, modeBeforeBadFlag: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"json", "human"} {
				t.Run(mode, func(t *testing.T) {
					root := t.TempDir()
					args := append(append([]string(nil), tt.args...), "--"+mode)
					if tt.modeBeforeBadFlag {
						args = append([]string{tt.args[0], "--" + mode}, tt.args[1:]...)
					}
					stdout, stderr, code := runTGProcessStreams(t, binary, root, env, args...)
					if code != 2 {
						t.Fatalf("exit code=%d, want BAD_ARGS=2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
					}
					if mode == "json" {
						if stderr != "" {
							t.Fatalf("JSON stderr=%q, want empty", stderr)
						}
						var envelope struct {
							OK      bool   `json:"ok"`
							Command string `json:"command"`
							Error   struct {
								Code string `json:"code"`
							} `json:"error"`
						}
						if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
							t.Fatalf("decode JSON stdout: %v\n%s", err, stdout)
						}
						if envelope.OK || envelope.Command != tt.command || envelope.Error.Code != "BAD_ARGS" {
							t.Fatalf("envelope=%+v, want command=%q BAD_ARGS", envelope, tt.command)
						}
					} else {
						if stdout != "" {
							t.Fatalf("human stdout=%q, want empty", stdout)
						}
						if strings.Count(stderr, "ERROR [BAD_ARGS]:") != 1 || strings.Count(strings.TrimSpace(stderr), "\n") != 0 {
							t.Fatalf("human stderr must contain one diagnostic line, got %q", stderr)
						}
					}
					entries, err := os.ReadDir(root)
					if err != nil {
						t.Fatal(err)
					}
					if len(entries) != 0 {
						t.Fatalf("malformed invocation created cwd artifacts: %v", entries)
					}
				})
			}
		})
	}
}

func TestProcessUnknownCommandsUseBadArgsWithoutArtifacts(t *testing.T) {
	binary := buildTGProcess(t)
	env := withoutStartupSafetyEnv(os.Environ())
	tests := []struct {
		name        string
		args        []string
		wantCommand string
		jsonMode    bool
		wantText    string
	}{
		{name: "root default human", args: []string{"unknown-command"}, wantCommand: "tg", wantText: `unknown command "unknown-command"`},
		{name: "root JSON", args: []string{"unknown-command", "--json"}, wantCommand: "tg", jsonMode: true, wantText: `unknown command "unknown-command"`},
		{name: "root suggestion", args: []string{"shwo"}, wantCommand: "tg", wantText: "Did you mean this?"},
		{name: "nested completion command", args: []string{"completion", "unknown-shell"}, wantCommand: "completion", wantText: `unknown command "unknown-shell"`},
		{name: "unsupported root human flag", args: []string{"--human"}, wantCommand: "tg", wantText: "unknown flag: --human"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			stdout, stderr, code := runTGProcessStreams(t, binary, root, env, tt.args...)
			if code != 2 {
				t.Fatalf("exit code=%d, want BAD_ARGS=2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if tt.jsonMode {
				if stderr != "" {
					t.Fatalf("JSON stderr=%q, want empty", stderr)
				}
				var envelope struct {
					Command string `json:"command"`
					Error   struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
					t.Fatalf("decode JSON stdout: %v\n%s", err, stdout)
				}
				if envelope.Command != tt.wantCommand || envelope.Error.Code != "BAD_ARGS" || !strings.Contains(envelope.Error.Message, tt.wantText) {
					t.Fatalf("envelope=%+v, want command=%q BAD_ARGS containing %q", envelope, tt.wantCommand, tt.wantText)
				}
			} else {
				if stdout != "" {
					t.Fatalf("human stdout=%q, want empty", stdout)
				}
				if strings.Count(stderr, "ERROR [BAD_ARGS]:") != 1 || !strings.Contains(stderr, tt.wantText) {
					t.Fatalf("human stderr=%q, want one BAD_ARGS containing %q", stderr, tt.wantText)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("malformed invocation created cwd artifacts: %v", entries)
			}
		})
	}
}

func TestProcessLinkerInjectedPrereleaseVersion(t *testing.T) {
	const want = "v0.0.0-rc.offline+build.7"
	binary := buildTGProcessWithVersion(t, want)
	root := t.TempDir()
	env := withoutStartupSafetyEnv(os.Environ())

	flagOut, flagCode := runTGProcessResult(t, binary, root, env, "--version")
	if flagCode != 0 {
		t.Fatalf("--version code=%d\n%s", flagCode, flagOut)
	}
	jsonOut, jsonCode := runTGProcessResult(t, binary, root, env, "version", "--json")
	if jsonCode != 0 {
		t.Fatalf("version --json code=%d\n%s", jsonCode, jsonOut)
	}
	var envelope struct {
		Data struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &envelope); err != nil {
		t.Fatalf("decode version envelope: %v\n%s", err, jsonOut)
	}
	if got := strings.TrimSpace(flagOut); got != want {
		t.Fatalf("--version = %q, want %q", got, want)
	}
	if got := envelope.Data.Version; got != want {
		t.Fatalf("version --json = %q, want %q", got, want)
	}
	if got := envelope.Data.Commit; got != "" {
		t.Fatalf("release fallback commit = %q, want empty", got)
	}
}

func TestProcessLinkerInjectedGitDescribeFallback(t *testing.T) {
	const injected = "v1.2.3-rc.1+build.5-12-gAbCdEf0-dirty"
	binary := buildTGProcessWithBuildInfo(t, injected, "", "git-describe")
	out, code := runTGProcessResult(t, binary, t.TempDir(), withoutStartupSafetyEnv(os.Environ()), "version", "--json")
	if code != 0 {
		t.Fatalf("version --json code=%d\n%s", code, out)
	}
	var envelope struct {
		Data struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode version envelope: %v\n%s", err, out)
	}
	if got, want := envelope.Data.Version, "v1.2.3-rc.1+build.5"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
	if got, want := envelope.Data.Commit, "AbCdEf0"; got != want {
		t.Fatalf("commit = %q, want %q", got, want)
	}
}

func TestProcessUnmarkedDescribeShapedReleaseIsPreserved(t *testing.T) {
	const injected = "v1.2.3-12-gabcdef0"
	binary := buildTGProcessWithBuildInfo(t, injected, "1234567890abcdef", "release")
	root := t.TempDir()
	env := withoutStartupSafetyEnv(os.Environ())

	flagOut, flagCode := runTGProcessResult(t, binary, root, env, "--version")
	if flagCode != 0 {
		t.Fatalf("--version code=%d\n%s", flagCode, flagOut)
	}
	jsonOut, jsonCode := runTGProcessResult(t, binary, root, env, "version", "--json")
	if jsonCode != 0 {
		t.Fatalf("version --json code=%d\n%s", jsonCode, jsonOut)
	}
	var envelope struct {
		Data struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &envelope); err != nil {
		t.Fatalf("decode version envelope: %v\n%s", err, jsonOut)
	}
	if got := strings.TrimSpace(flagOut); got != injected {
		t.Fatalf("--version = %q, want %q", got, injected)
	}
	if got := envelope.Data.Version; got != injected {
		t.Fatalf("version --json = %q, want %q", got, injected)
	}
	if got, want := envelope.Data.Commit, "1234567890ab"; got != want {
		t.Fatalf("commit = %q, want %q", got, want)
	}
}

func TestProcessVersionAndDoctorSurfacesAgree(t *testing.T) {
	tests := []struct {
		name    string
		version string
		source  string
		want    string
	}{
		{name: "marked git describe", version: "v1.2.3-12-gabcdef0", source: "git-describe", want: "v1.2.3"},
		{name: "unmarked describe-shaped release", version: "v1.2.3-12-gabcdef0", source: "release", want: "v1.2.3-12-gabcdef0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary := buildTGProcessWithBuildInfo(t, tt.version, "1234567890abcdef", tt.source)
			root := t.TempDir()
			env := withoutStartupSafetyEnv(os.Environ())

			flagOut, code := runTGProcessResult(t, binary, root, env, "--version")
			if code != 0 || strings.TrimSpace(flagOut) != tt.want {
				t.Fatalf("--version code=%d output=%q, want %q", code, flagOut, tt.want)
			}

			for _, command := range []string{"version", "doctor"} {
				for _, mode := range []string{"--human", "--json"} {
					out, code := runTGProcessResult(t, binary, root, env, command, mode)
					if code != 0 {
						t.Fatalf("%s %s code=%d\n%s", command, mode, code, out)
					}
					var decoded map[string]any
					if err := json.Unmarshal([]byte(out), &decoded); err != nil {
						t.Fatalf("decode %s %s: %v\n%s", command, mode, err, out)
					}
					data := decoded
					if mode == "--json" {
						var ok bool
						data, ok = decoded["data"].(map[string]any)
						if !ok {
							t.Fatalf("%s %s missing data object: %#v", command, mode, decoded)
						}
					}
					if got := data["version"]; got != tt.want {
						t.Fatalf("%s %s version = %q, want %q", command, mode, got, tt.want)
					}
					if command == "doctor" {
						if _, ok := data["commit"]; ok {
							t.Fatalf("doctor %s unexpectedly exposes commit", mode)
						}
						if _, ok := data["version_source"]; ok {
							t.Fatalf("doctor %s unexpectedly exposes version source", mode)
						}
					}
				}
			}
		})
	}
}

func withoutEnv(environ []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(environ))
	for _, item := range environ {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return out
}

func withoutStartupSafetyEnv(environ []string) []string {
	return withoutEnv(withoutEnv(environ, "TG_READONLY"), "TG_ALLOW_WRITE")
}

func assertLegacyUnchanged(t *testing.T, root string, state legacyState) {
	t.Helper()
	for name, want := range state.files {
		path := filepath.Join(root, name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before := state.infos[name]
		if !bytes.Equal(got, want) || !os.SameFile(before, info) || before.Mode() != info.Mode() || before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) {
			t.Fatalf("legacy path changed: %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "accounts")); !os.IsNotExist(err) {
		t.Fatalf("accounts directory created: %v", err)
	}
}

func assertWriteDisallowedEnvelope(t *testing.T, output string) {
	t.Helper()
	var envelope struct {
		OK        bool   `json:"ok"`
		Command   string `json:"command"`
		RequestID string `json:"request_id"`
		Error     struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, output)
	}
	if envelope.OK || envelope.Command != "download-media" || envelope.Error.Code != "WRITE_DISALLOWED" || !strings.HasPrefix(envelope.RequestID, "req-") {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
}

func TestProcessDownloadMediaMissingAllowSkipsLegacyMigration(t *testing.T) {
	binary := buildTGProcess(t)
	for _, args := range [][]string{
		{"download-media", "1", "9", "--json"},
		{"download-media", "--allow-write=false", "1", "9", "--json"},
		{"download-media", "1", "9", "--allow-write=false", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			out, code := runTGProcessResult(t, binary, root, withoutStartupSafetyEnv(os.Environ()), args...)
			if code != 6 {
				t.Fatalf("code=%d want 6\n%s", code, out)
			}
			assertWriteDisallowedEnvelope(t, out)
			assertLegacyUnchanged(t, root, state)
		})
	}
}

func TestProcessDownloadMediaReadOnlyAllowSkipsLegacyMigration(t *testing.T) {
	binary := buildTGProcess(t)
	baseEnv := withoutStartupSafetyEnv(os.Environ())
	tests := []struct {
		name   string
		args   []string
		env    []string
		dotEnv string
	}{
		{name: "global flag before", args: []string{"--read-only", "download-media", "1", "9", "--allow-write", "--json"}, env: baseEnv},
		{name: "global flag after", args: []string{"download-media", "1", "9", "--allow-write", "--read-only", "--json"}, env: baseEnv},
		{name: "environment", args: []string{"download-media", "1", "9", "--allow-write", "--json"}, env: append(baseEnv, "TG_READONLY=1")},
		{name: "dotenv", args: []string{"download-media", "1", "9", "--allow-write", "--json"}, env: baseEnv, dotEnv: "TG_READONLY=yes\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			if tt.dotEnv != "" {
				if err := os.WriteFile(filepath.Join(root, ".env"), []byte(tt.dotEnv), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, code := runTGProcessResult(t, binary, root, tt.env, tt.args...)
			if code != 6 {
				t.Fatalf("code=%d want 6\n%s", code, out)
			}
			assertWriteDisallowedEnvelope(t, out)
			assertLegacyUnchanged(t, root, state)
		})
	}
}

func TestProcessAuthorizedDownloadMediaMigratesBeforeLeafExecution(t *testing.T) {
	binary := buildTGProcess(t)
	for _, args := range [][]string{
		{"download-media", "1", "9", "--allow-write", "--json"},
		{"download-media", "--allow-write", "1", "9", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			out, code := runTGProcessResult(t, binary, root, withoutStartupSafetyEnv(os.Environ()), args...)
			if code == 0 || code == 6 {
				t.Fatalf("code=%d want later command failure after authorization\n%s", code, out)
			}
			assertLegacyMigrated(t, root, state)
		})
	}
}

func TestProcessMigrationErrorWarnsAndContinues(t *testing.T) {
	binary := buildTGProcess(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "telegram.sqlite"), []byte("legacy-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "accounts"), []byte("blocks directory creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runTGProcessResult(t, binary, root, withoutStartupSafetyEnv(os.Environ()), "version", "--json")
	if code != 0 {
		t.Fatalf("version code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "WARN: account migration failed:") || !strings.Contains(out, `"command":"version"`) {
		t.Fatalf("warning or successful command output missing:\n%s", out)
	}
}

func TestProcessConcurrentAccountSelectionSnapshotsNeverFallBackDefault(t *testing.T) {
	binary := buildTGProcess(t)
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	env := withoutStartupSafetyEnv(os.Environ())
	for _, args := range [][]string{{"accounts-add", "a", "--json"}, {"accounts-add", "b", "--json"}, {"accounts-use", "a", "--json"}} {
		if out, code := runTGProcessResult(t, binary, root, env, args...); code != 0 {
			t.Fatalf("setup %v code=%d out=%s", args, code, out)
		}
	}

	start := make(chan struct{})
	errCh := make(chan error, 16)
	var wg sync.WaitGroup
	for writer := 0; writer < 2; writer++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for i := 0; i < 12; i++ {
				name := []string{"a", "b"}[(i+offset)%2]
				cmd := exec.Command(binary, "accounts-use", name, "--json")
				cmd.Dir, cmd.Env = root, env
				if out, err := cmd.CombinedOutput(); err != nil {
					errCh <- fmt.Errorf("accounts-use %s: %w: %s", name, err, out)
					return
				}
			}
		}(writer)
	}
	for reader := 0; reader < 3; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 20; i++ {
				cmd := exec.Command(binary, "accounts-show", "--json")
				cmd.Dir, cmd.Env = root, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					errCh <- fmt.Errorf("accounts-show: %w: %s", err, out)
					return
				}
				var envelope struct {
					Data struct {
						Name        string `json:"name"`
						DBPath      string `json:"db_path"`
						SessionPath string `json:"session_path"`
						AuditPath   string `json:"audit_path"`
						MediaDir    string `json:"media_dir"`
					} `json:"data"`
				}
				if err := json.Unmarshal(out, &envelope); err != nil {
					errCh <- fmt.Errorf("decode accounts-show: %w: %s", err, out)
					return
				}
				name := envelope.Data.Name
				if name != "a" && name != "b" {
					errCh <- fmt.Errorf("observed invalid current account %q", name)
					return
				}
				wantDir := filepath.Join(realRoot, "accounts", name)
				for label, got := range map[string]string{
					"db": envelope.Data.DBPath, "session": envelope.Data.SessionPath,
					"audit": envelope.Data.AuditPath, "media": envelope.Data.MediaDir,
				} {
					if filepath.Dir(got) != wantDir && !(label == "media" && got == filepath.Join(wantDir, "media")) {
						errCh <- fmt.Errorf("%s path %q inconsistent with account %q", label, got, name)
						return
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProcessInformationalHelpAndBuiltinVersionDoNotMigrate(t *testing.T) {
	binary := buildTGProcess(t)
	for _, args := range [][]string{{"--help"}, {"--version"}, {"download-media", "--help"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			if out, code := runTGProcessResult(t, binary, root, withoutStartupSafetyEnv(os.Environ()), args...); code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			assertLegacyUnchanged(t, root, state)
		})
	}
}

func assertLegacyMigrated(t *testing.T, root string, state legacyState) {
	t.Helper()
	for name, want := range state.files {
		destination := filepath.Join(root, "accounts", "default", name)
		got, err := os.ReadFile(destination)
		if err != nil {
			t.Fatalf("read migrated %s: %v", name, err)
		}
		if name == "audit.log" {
			if !bytes.HasPrefix(got, want) {
				t.Fatalf("migrated audit lost legacy prefix: %q", got)
			}
			continue
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("migrated %s = %q, want %q", name, got, want)
		}
	}
}

func TestProcessStartupReadOnlySkipsLegacyMigration(t *testing.T) {
	binary := buildTGProcess(t)
	baseEnv := withoutEnv(os.Environ(), "TG_READONLY")
	tests := []struct {
		name   string
		args   []string
		env    []string
		dotEnv string
	}{
		{name: "flag before command", args: []string{"--read-only", "version", "--json"}, env: baseEnv},
		{name: "flag after command", args: []string{"version", "--read-only", "--json"}, env: baseEnv},
		{name: "explicit true", args: []string{"--read-only=true", "version", "--json"}, env: baseEnv},
		{name: "environment true", args: []string{"version", "--json"}, env: append(baseEnv, "TG_READONLY=true")},
		{name: "dotenv truthy", args: []string{"version", "--json"}, env: baseEnv, dotEnv: "TG_READONLY=yes\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			if tt.dotEnv != "" {
				if err := os.WriteFile(filepath.Join(root, ".env"), []byte(tt.dotEnv), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runTGProcess(t, binary, root, tt.env, tt.args...)
			assertLegacyUnchanged(t, root, state)
		})
	}
}

func TestProcessStartupNormalModeStillMigratesLegacyState(t *testing.T) {
	binary := buildTGProcess(t)
	root := t.TempDir()
	state := seedLegacyState(t, root)
	runTGProcess(t, binary, root, withoutEnv(os.Environ(), "TG_READONLY"), "version", "--json")
	for name, want := range state.files {
		destination := filepath.Join(root, "accounts", "default", name)
		if name == "tg.session.lock" {
			destination = filepath.Join(root, "accounts", "default", "tg.session.lock")
		} else if strings.HasPrefix(name, "media/") {
			destination = filepath.Join(root, "accounts", "default", name)
		}
		got, err := os.ReadFile(destination)
		if err != nil {
			t.Fatalf("read migrated %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("migrated %s = %q, want %q", name, got, want)
		}
	}
}

func TestProcessStartupFalseBooleanSpellingsStillMigrateLegacyState(t *testing.T) {
	binary := buildTGProcess(t)
	for _, spelling := range []string{"f", "F", "false", "0"} {
		t.Run(spelling, func(t *testing.T) {
			root := t.TempDir()
			state := seedLegacyState(t, root)
			runTGProcess(t, binary, root, withoutEnv(os.Environ(), "TG_READONLY"), "--read-only="+spelling, "version", "--json")
			for name, want := range state.files {
				destination := filepath.Join(root, "accounts", "default", name)
				got, err := os.ReadFile(destination)
				if err != nil {
					t.Fatalf("read migrated %s: %v", name, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("migrated %s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestProcessStartupMalformedReadOnlyFailsSafeAndLetsCobraRejectIt(t *testing.T) {
	binary := buildTGProcess(t)
	root := t.TempDir()
	state := seedLegacyState(t, root)
	cmd := exec.Command(binary, "--read-only=malformed", "version", "--json")
	cmd.Dir = root
	cmd.Env = withoutEnv(os.Environ(), "TG_READONLY")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("malformed boolean unexpectedly succeeded: %s", out)
	}
	assertLegacyUnchanged(t, root, state)
}
