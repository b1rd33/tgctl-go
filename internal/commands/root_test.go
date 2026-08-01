package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestNewRootCommandHasNameAndNoCommandReturnsOK(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if root.Use != "tg" {
		t.Fatalf("Use = %q, want tg", root.Use)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("Telegram agent CLI")) {
		t.Fatalf("stderr help = %q, want description", stderr.String())
	}
}

func TestRootCommandGlobalFlags(t *testing.T) {
	root := NewRootCommand()

	flags := []string{"read-only", "lock-wait", "full", "account"}
	for _, name := range flags {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Fatalf("missing persistent flag --%s", name)
		}
	}
}

func TestRootCommandPropagatesGlobalFlagValues(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"--read-only", "--lock-wait", "1.5", "--full", "--account", "work"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	cfg := RootConfigFrom(root)
	if !cfg.ReadOnly {
		t.Fatalf("ReadOnly = false")
	}
	if cfg.LockWaitSeconds != 1.5 {
		t.Fatalf("LockWaitSeconds = %v, want 1.5", cfg.LockWaitSeconds)
	}
	if !cfg.Full {
		t.Fatalf("Full = false")
	}
	if cfg.Account != "work" {
		t.Fatalf("Account = %q, want work", cfg.Account)
	}
}

func TestVersionCommandRegistered(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"version", "--json"})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"command":"version"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"warnings":[]`)) {
		t.Fatalf("stdout = %q, want success envelope with warnings", stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"commit"`)) {
		t.Fatalf("stdout = %q, want commit field", stdout.String())
	}
}

func TestRootVersionFlagUsesInjectedVersion(t *testing.T) {
	old := Version
	Version = "v0.1.0-2-gabcdef0"
	defer func() { Version = old }()

	var stdout bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--version"})

	code := ExecuteRoot(root)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := stdout.String(); got != "v0.1.0\n" {
		t.Fatalf("stdout = %q, want v0.1.0", got)
	}
}

func TestSemverVersionPreservesReleasesAndRecognizesGitDescribe(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "release", version: "v1.2.3", want: "v1.2.3"},
		{name: "prerelease", version: "v1.2.3-rc.1", want: "v1.2.3-rc.1"},
		{name: "dotted and hyphenated prerelease", version: "v1.2.3-alpha-beta.1", want: "v1.2.3-alpha-beta.1"},
		{name: "prerelease with git-like identifier", version: "v1.2.3-alpha-gold", want: "v1.2.3-alpha-gold"},
		{name: "build metadata", version: "v1.2.3+build.5", want: "v1.2.3+build.5"},
		{name: "prerelease and build metadata", version: "v1.2.3-rc.1+build.5", want: "v1.2.3-rc.1+build.5"},
		{name: "git describe", version: "v1.2.3-12-gabcdef0", want: "v1.2.3"},
		{name: "git describe prerelease tag", version: "v1.2.3-rc.1-12-gabcdef0", want: "v1.2.3-rc.1"},
		{name: "dirty git describe", version: "v1.2.3-12-gabcdef0-dirty", want: "v1.2.3"},
		{name: "dev", version: "dev", want: "dev"},
		{name: "empty", version: "", want: ""},
		{name: "bare commit", version: "abcdef0", want: "abcdef0"},
		{name: "unprefixed prerelease", version: "1.2.3-rc.1", want: "1.2.3-rc.1"},
		{name: "invalid short version", version: "v1.2", want: "v1.2"},
		{name: "invalid prerelease", version: "v1.2.3-", want: "v1.2.3-"},
		{name: "invalid git describe base", version: "v01.2.3-12-gabcdef0", want: "v01.2.3-12-gabcdef0"},
		{name: "invalid git describe hash", version: "v1.2.3-12-gnothex", want: "v1.2.3-12-gnothex"},
		{name: "non-version git describe", version: "release-12-gabcdef0", want: "release-12-gabcdef0"},
		{name: "opaque v prefix", version: "version-candidate", want: "version-candidate"},
	}

	old := Version
	defer func() { Version = old }()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			if got := semverVersion(); got != tt.want {
				t.Fatalf("semverVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionCommandAndBuiltinFlagAgree(t *testing.T) {
	old := Version
	Version = "v1.2.3-rc.1+build.5"
	defer func() { Version = old }()

	var flagOut bytes.Buffer
	flagRoot := NewRootCommand()
	flagRoot.SetOut(&flagOut)
	flagRoot.SetErr(io.Discard)
	flagRoot.SetArgs([]string{"--version"})
	if code := ExecuteRoot(flagRoot); code != 0 {
		t.Fatalf("--version exit code = %d, want 0", code)
	}

	var jsonOut bytes.Buffer
	jsonRoot := NewRootCommand()
	jsonRoot.SetOut(&jsonOut)
	jsonRoot.SetErr(io.Discard)
	jsonRoot.SetArgs([]string{"version", "--json"})
	if code := ExecuteRoot(jsonRoot); code != 0 {
		t.Fatalf("version --json exit code = %d, want 0", code)
	}
	var envelope struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &envelope); err != nil {
		t.Fatalf("decode version envelope: %v\n%s", err, jsonOut.String())
	}
	if got, want := strings.TrimSpace(flagOut.String()), envelope.Data.Version; got != want {
		t.Fatalf("--version = %q, version --json = %q", got, want)
	}
}
