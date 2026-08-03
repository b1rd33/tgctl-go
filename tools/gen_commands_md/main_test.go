package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratorUsesSelectedBinaryAndRendersDynamicReference(t *testing.T) {
	binary := buildFakeTG(t)

	first, err := runGenerator(t, binary)
	if err != nil {
		t.Fatalf("first generator run: %v\n%s", err, first)
	}
	second, err := runGenerator(t, binary)
	if err != nil {
		t.Fatalf("second generator run: %v\n%s", err, second)
	}
	if first != second {
		t.Fatal("generator output is not deterministic")
	}
	if strings.HasSuffix(first, "\n\n") {
		t.Error("generator output has an extra blank line at EOF")
	}

	wants := []string{
		"`tg --help` shows 4 commands. This page is generated from Cobra help output.",
		"tg discover --allow-write --json",
		"tg backfill-entities --allow-write --json",
		"tg download-media 123456789 42 --max-size-mb 100 --allow-write --json",
		"| [`tg import-telethon-session`]",
	}
	for _, want := range wants {
		if !strings.Contains(first, want) {
			t.Errorf("output does not contain %q", want)
		}
	}

	backfill := strings.Index(first, "| [`tg backfill-entities`]")
	discover := strings.Index(first, "| [`tg discover`]")
	download := strings.Index(first, "| [`tg download-media`]")
	if backfill < 0 || discover < 0 || download < 0 || !(backfill < discover && discover < download) {
		t.Errorf("index is not sorted: backfill=%d discover=%d download=%d", backfill, discover, download)
	}
}

func TestGeneratorRejectsWhitespaceBinarySelection(t *testing.T) {
	output, err := runGenerator(t, " \t ")
	if err == nil {
		t.Fatalf("generator succeeded with whitespace TGCTL_DOCS_BINARY:\n%s", output)
	}
	if !strings.Contains(output, "TGCTL_DOCS_BINARY") || !strings.Contains(strings.ToLower(output), "empty") {
		t.Fatalf("error is not actionable:\n%s", output)
	}
}

func TestGeneratorFallsBackToLocalBinaryWhenSelectionIsUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fallback executable is named ./tg")
	}
	binary := buildFakeTG(t)
	fallback := filepath.Join(filepath.Dir(binary), "tg")
	if err := os.Rename(binary, fallback); err != nil {
		t.Fatal(err)
	}
	mainFile, err := filepath.Abs("main.go")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", mainFile)
	cmd.Dir = filepath.Dir(fallback)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TGCTL_DOCS_BINARY=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generator fallback: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "`tg --help` shows 4 commands.") {
		t.Fatalf("generator did not use ./tg fallback:\n%s", output)
	}
}

func TestDocsCommandsTargetsBuildCurrentSourceWithoutRepositoryBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipe uses a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	for _, target := range []string{"docs-commands", "docs-commands-check"} {
		for _, staleRepositoryBinary := range []bool{false, true} {
			name := target + "/without tg"
			if staleRepositoryBinary {
				name = target + "/with stale tg"
			}
			t.Run(name, func(t *testing.T) {
				repository := copyTrackedRepository(t)
				reference := filepath.Join(repository, "docs", "commands.md")
				before, err := os.ReadFile(reference)
				if err != nil {
					t.Fatal(err)
				}

				fake := buildFakeTG(t)
				if staleRepositoryBinary {
					copyFile(t, fake, filepath.Join(repository, "tg"))
				}
				temporaryRoot := t.TempDir()
				cmd := exec.Command("make", target)
				cmd.Dir = repository
				cmd.Env = append(os.Environ(),
					"TMPDIR="+temporaryRoot,
					"TGCTL_DOCS_BINARY="+fake,
				)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("standalone %s: %v\n%s", target, err, output)
				}

				after, err := os.ReadFile(reference)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after, before) {
					t.Fatal("docs-commands-check modified docs/commands.md")
				}
				if target == "docs-commands" {
					info, err := os.Stat(reference)
					if err != nil {
						t.Fatal(err)
					}
					if got := info.Mode().Perm(); got != 0o644 {
						t.Fatalf("published mode = %04o, want 0644", got)
					}
				}
				entries, err := os.ReadDir(temporaryRoot)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "tgctl-docs.") {
						t.Fatalf("target temporary path was not cleaned up: %v", entries)
					}
				}
				if !staleRepositoryBinary {
					if _, err := os.Stat(filepath.Join(repository, "tg")); !os.IsNotExist(err) {
						t.Fatalf("target created repository tg: %v", err)
					}
				}
			})
		}
	}
}

func TestDocsCommandsCreatesMissingReferenceWithRepositoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipe uses a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	repository := copyTrackedRepository(t)
	reference := filepath.Join(repository, "docs", "commands.md")
	want, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(reference); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("make", "docs-commands")
	cmd.Dir = repository
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standalone docs-commands: %v\n%s", err, output)
	}
	got, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("newly generated command reference differs from current source")
	}
	info, err := os.Stat(reference)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("new reference mode = %04o, want 0644", got)
	}
}

func TestDocsCommandsCleansBuildDirectoryWhenOutputTempCannotBeCreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipe uses a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	repository := copyTrackedRepository(t)
	docsDir := filepath.Join(repository, "docs")
	if err := os.Chmod(docsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(docsDir, 0o755) })
	temporaryRoot := t.TempDir()
	cmd := exec.Command("make", "docs-commands")
	cmd.Dir = repository
	cmd.Env = append(os.Environ(), "TMPDIR="+temporaryRoot)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("docs-commands unexpectedly succeeded with read-only docs directory:\n%s", output)
	}
	entries, err := os.ReadDir(temporaryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tgctl-docs.") {
			t.Fatalf("build directory survived failed output temp creation: %v", entries)
		}
	}
}

func TestDocsCommandsCheckReportsDriftWithoutChangingReference(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipe uses a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	repository := copyTrackedRepository(t)
	reference := filepath.Join(repository, "docs", "commands.md")
	before, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	before = append(before, []byte("\ndeliberate drift\n")...)
	if err := os.WriteFile(reference, before, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("make", "docs-commands-check")
	cmd.Dir = repository
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("drift check unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "docs/commands.md is out of date") {
		t.Fatalf("drift error is not actionable:\n%s", output)
	}
	after, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("docs-commands-check changed the drifted reference")
	}
}

func copyTrackedRepository(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = source
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	for _, relativeBytes := range bytes.Split(output, []byte{0}) {
		if len(relativeBytes) == 0 {
			continue
		}
		relative := string(relativeBytes)
		copyFile(t, filepath.Join(source, relative), filepath.Join(destination, relative))
	}
	return destination
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func runGenerator(t *testing.T, binary string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "run", "main.go")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "TGCTL_DOCS_BINARY="+binary)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func buildFakeTG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := strings.Join(os.Args[1:], " ")
	switch args {
	case "--help":
		fmt.Print("Available Commands:\n  download-media          Download media attached to a message\n  discover                Discover dialogs\n  import-telethon-session Adopt a session\n  backfill-entities       Populate entity cache\n\nFlags:\n  -h, --help  help\n")
	case "backfill-entities --help":
		fmt.Print("Populate entity cache\n\nUsage:\n  tg backfill-entities [flags]\n\nFlags:\n  --allow-write  Required\n")
	case "discover --help":
		fmt.Print("Discover dialogs\n\nUsage:\n  tg discover [flags]\n\nFlags:\n  --allow-write  Required\n")
	case "download-media --help":
		fmt.Print("Download media attached to a message\n\nUsage:\n  tg download-media <chat> <message-id> [flags]\n\nFlags:\n  --allow-write       Required\n  --max-size-mb int   Maximum size\n  --output string     Output directory\n  --overwrite         Overwrite\n")
	case "import-telethon-session --help":
		fmt.Print("Adopt a session\n\nUsage:\n  tg import-telethon-session <path> [flags]\n\nFlags:\n  --json  JSON output\n")
	default:
		fmt.Fprintln(os.Stderr, "unexpected arguments:", args)
		os.Exit(2)
	}
}
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "fake-tg")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake tg: %v\n%s", err, out)
	}
	return binary
}
