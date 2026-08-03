package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupWritesPrivateEnvAndPreservesUnrelatedValues(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("TG_API_ID=old\nOTHER=value\nTG_API_HASH=oldhash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "setup", "--env-file", envPath, "--api-id", "12345", "--api-hash", "abcdef0123456789abcdef0123456789", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "TG_API_ID=12345") || !strings.Contains(text, "TG_API_HASH=abcdef0123456789abcdef0123456789") || !strings.Contains(text, "OTHER=value") {
		t.Fatalf("env contents=%q", text)
	}
	if strings.Contains(out, "abcdef0123456789abcdef0123456789") {
		t.Fatalf("hash leaked in output: %s", out)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(envPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode=%#o want 0600", got)
		}
	}
}

func TestSetupRejectsMalformedCredentialsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	cfg, _, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "setup", "--env-file", envPath, "--api-id", "not-a-number", "--api-hash", "secret", "--json")
	if code != 2 || !strings.Contains(out, "positive 32-bit") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("malformed setup created env: %v", err)
	}
}

func TestSetupIsBlockedInReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	cfg, _, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "--read-only", "setup", "--env-file", envPath, "--api-id", "12345", "--api-hash", "abcdef0123456789abcdef0123456789", "--json")
	if code != 6 || !strings.Contains(out, "WRITE_DISALLOWED") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}
