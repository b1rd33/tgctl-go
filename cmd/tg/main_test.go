package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestStartupReadOnlyRecognizesDocumentedBooleanSpellings(t *testing.T) {
	for _, spelling := range []string{"f", "F", "false", "0", "no", "off"} {
		if startupReadOnly([]string{"--read-only=" + spelling}) {
			t.Errorf("--read-only=%s treated as true", spelling)
		}
	}
	for _, spelling := range []string{"t", "T", "true", "1", "yes", "on"} {
		if !startupReadOnly([]string{"--read-only=" + spelling}) {
			t.Errorf("--read-only=%s treated as false", spelling)
		}
	}
	if !startupReadOnly([]string{"--read-only=malformed"}) {
		t.Error("malformed boolean must fail safe as read-only before Cobra reports it")
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
