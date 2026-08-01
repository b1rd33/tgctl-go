package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
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

func TestExecuteRootUnknownCommandDefaultsToHumanBadArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCommand()
	root.AddCommand(&cobra.Command{Use: "known", Run: func(*cobra.Command, []string) {}})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"unknown"})

	if code := ExecuteRoot(root); code != 2 {
		t.Fatalf("exit code=%d, want BAD_ARGS=2", code)
	}
	if stdout.Len() != 0 || strings.Count(stderr.String(), "ERROR [BAD_ARGS]:") != 1 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
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
	if got := stdout.String(); got != "v0.1.0-2-gabcdef0\n" {
		t.Fatalf("stdout = %q, want unmarked version unchanged", got)
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
		{name: "unmarked describe-shaped semver", version: "v1.2.3-12-gabcdef0", want: "v1.2.3-12-gabcdef0"},
		{name: "unmarked described prerelease tag", version: "v1.2.3-rc.1-12-gabcdef0", want: "v1.2.3-rc.1-12-gabcdef0"},
		{name: "unmarked dirty description", version: "v1.2.3-12-gabcdef0-dirty", want: "v1.2.3-12-gabcdef0-dirty"},
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

func TestSemverVersionOnlyNormalizesMarkedGitDescribe(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "release base", version: "v1.2.3-12-gabcdef0", want: "v1.2.3"},
		{name: "prerelease base", version: "v1.2.3-rc.1-12-gabcdef0", want: "v1.2.3-rc.1"},
		{name: "build metadata base", version: "v1.2.3+build.5-12-gabcdef0", want: "v1.2.3+build.5"},
		{name: "dirty", version: "v1.2.3-12-gabcdef0-dirty", want: "v1.2.3"},
		{name: "bare clean", version: "ABCDEF0", want: "ABCDEF0"},
		{name: "bare dirty", version: "ABCDEF0-dirty", want: "ABCDEF0-dirty"},
		{name: "zero distance rejected", version: "v1.2.3-0-gabcdef0", want: "v1.2.3-0-gabcdef0"},
		{name: "leading zero distance rejected", version: "v1.2.3-01-gabcdef0", want: "v1.2.3-01-gabcdef0"},
		{name: "malformed preserved", version: "v1.2-12-gabcdef0", want: "v1.2-12-gabcdef0"},
		{name: "dev", version: "dev", want: "dev"},
		{name: "empty", version: "", want: ""},
	}

	oldVersion, oldSource := Version, VersionSource
	defer func() { Version, VersionSource = oldVersion, oldSource }()
	VersionSource = "git-describe"
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
	old, oldCommit, oldSource := Version, Commit, VersionSource
	Version = "v1.2.3-rc.1+build.5"
	Commit = ""
	VersionSource = "release"
	defer func() { Version, Commit, VersionSource = old, oldCommit, oldSource }()

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
			Commit  string `json:"commit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &envelope); err != nil {
		t.Fatalf("decode version envelope: %v\n%s", err, jsonOut.String())
	}
	if got, want := strings.TrimSpace(flagOut.String()), envelope.Data.Version; got != want {
		t.Fatalf("--version = %q, version --json = %q", got, want)
	}

	var humanOut bytes.Buffer
	humanRoot := NewRootCommand()
	humanRoot.SetOut(&humanOut)
	humanRoot.SetErr(io.Discard)
	humanRoot.SetArgs([]string{"version", "--human"})
	if code := ExecuteRoot(humanRoot); code != 0 {
		t.Fatalf("version --human exit code = %d, want 0", code)
	}
	var humanData struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(humanOut.Bytes(), &humanData); err != nil {
		t.Fatalf("decode human version output: %v\n%s", err, humanOut.String())
	}
	if got, want := humanData.Version, envelope.Data.Version; got != want {
		t.Fatalf("version --human = %q, version --json = %q", got, want)
	}
	if got, want := humanData.Commit, envelope.Data.Commit; got != want {
		t.Fatalf("version --human commit = %q, version --json commit = %q", got, want)
	}
}

func TestShortCommitFallback(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		explicitCommit string
		source         string
		want           string
	}{
		{name: "unmarked described release", version: "v1.2.3-12-gabcdef0", want: ""},
		{name: "described release", version: "v1.2.3-12-gabcdef0", source: "git-describe", want: "abcdef0"},
		{name: "described prerelease", version: "v1.2.3-rc.1-12-gabcdef0", source: "git-describe", want: "abcdef0"},
		{name: "described build metadata", version: "v1.2.3+build.5-12-gabcdef0", source: "git-describe", want: "abcdef0"},
		{name: "dirty description", version: "v1.2.3-12-gabcdef0-dirty", source: "git-describe", want: "abcdef0"},
		{name: "mixed case hex", version: "v1.2.3-12-gAbCdEf0", source: "git-describe", want: "AbCdEf0"},
		{name: "bare clean", version: "ABCDEF0", source: "git-describe", want: "ABCDEF0"},
		{name: "bare dirty", version: "ABCDEF0-dirty", source: "git-describe", want: "ABCDEF0"},
		{name: "zero distance", version: "v1.2.3-0-gabcdef0", source: "git-describe", want: ""},
		{name: "leading zero distance", version: "v1.2.3-01-gabcdef0", source: "git-describe", want: ""},
		{name: "nondecimal count", version: "v1.2.3-many-gabcdef0", source: "git-describe", want: ""},
		{name: "nonhex commit", version: "v1.2.3-12-gnothex", source: "git-describe", want: ""},
		{name: "malformed version", version: "v1.2-12-gabcdef0", source: "git-describe", want: ""},
		{name: "valid prerelease", version: "v1.2.3-rc.1", want: ""},
		{name: "valid release with build metadata", version: "v1.2.3+build.5", want: ""},
		{name: "unmarked opaque", version: "release-candidate", want: ""},
		{name: "dev", version: "dev", want: ""},
		{name: "empty", version: "", want: ""},
		{name: "unmarked bare commit", version: "abcdef0", want: ""},
		{name: "explicit commit", version: "v1.2.3", explicitCommit: "1234567890abcdef", want: "1234567890ab"},
	}

	if revision := vcsRevision(); revision != "" {
		t.Fatalf("unit test binary unexpectedly embeds vcs.revision %q; fallback would be masked", revision)
	}
	old, oldCommit, oldSource := Version, Commit, VersionSource
	defer func() { Version, Commit, VersionSource = old, oldCommit, oldSource }()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			Commit = tt.explicitCommit
			VersionSource = tt.source
			if got := shortCommit(); got != tt.want {
				t.Fatalf("shortCommit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectShortCommitPrecedence(t *testing.T) {
	const (
		described = "v1.2.3-12-gabcdef0"
		explicit  = "1111111111111111"
		revision  = "2222222222222222"
	)
	if got, want := selectShortCommit(described, explicit, "git-describe", revision), "111111111111"; got != want {
		t.Fatalf("explicit commit precedence = %q, want %q", got, want)
	}
	if got, want := selectShortCommit(described, "", "git-describe", revision), "222222222222"; got != want {
		t.Fatalf("build-info revision precedence = %q, want %q", got, want)
	}
	if got, want := selectShortCommit(described, "", "git-describe", ""), "abcdef0"; got != want {
		t.Fatalf("git-describe fallback = %q, want %q", got, want)
	}
}

func TestDoctorReportUsesProvenanceAwareDisplayVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		source  string
		want    string
	}{
		{name: "marked git describe", version: "v1.2.3-12-gabcdef0", source: "git-describe", want: "v1.2.3"},
		{name: "unmarked describe-shaped release", version: "v1.2.3-12-gabcdef0", source: "release", want: "v1.2.3-12-gabcdef0"},
	}

	oldVersion, oldSource := Version, VersionSource
	defer func() { Version, VersionSource = oldVersion, oldSource }()
	manager := accounts.New(t.TempDir())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, VersionSource = tt.version, tt.source
			report, err := doctorReport(manager, "default")
			if err != nil {
				t.Fatal(err)
			}
			if got := report["version"]; got != tt.want {
				t.Fatalf("doctor version = %q, want %q", got, tt.want)
			}
			if _, ok := report["commit"]; ok {
				t.Fatal("doctor unexpectedly exposes commit")
			}
			if _, ok := report["version_source"]; ok {
				t.Fatal("doctor unexpectedly exposes version source")
			}
		})
	}
}
