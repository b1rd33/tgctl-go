package main_test

import (
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
		"tg download-media 1240314255 42 --max-size-mb 100 --allow-write --json",
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

func TestDocsCommandsCheckReportsDriftWithoutModifyingReference(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Make recipe uses a POSIX shell")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	repository := filepath.Clean(filepath.Join("..", ".."))
	reference := filepath.Join(repository, "docs", "commands.md")
	before, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("make", "docs-commands-check")
	cmd.Dir = repository
	cmd.Env = append(os.Environ(), "TGCTL_DOCS_BINARY="+buildFakeTG(t))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("drift check unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "docs/commands.md is out of date") {
		t.Fatalf("drift error is not actionable:\n%s", output)
	}

	after, readErr := os.ReadFile(reference)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatal("docs-commands-check modified docs/commands.md")
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
