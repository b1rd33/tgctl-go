package dispatch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

func readAuditEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	s := bufio.NewScanner(f)
	for s.Scan() {
		var m map[string]any
		_ = json.Unmarshal(s.Bytes(), &m)
		out = append(out, m)
	}
	return out
}

func TestRunSuccessJSONReturnsZeroAndAudits(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.log")
	var stdout, stderr bytes.Buffer

	code := Run("stats", Options{
		JSON:      true,
		Stdout:    &stdout,
		Stderr:    &stderr,
		AuditPath: auditPath,
		Args:      map[string]any{"limit": 10},
	}, func(ctx context.Context) (any, error) {
		if RequestIDFrom(ctx) == "" {
			t.Fatalf("runner missing request id")
		}
		return map[string]any{"chats": 5}, nil
	})

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 || entries[0]["result"] != "ok" || entries[0]["cmd"] != "stats" {
		t.Fatalf("audit = %#v", entries)
	}
}

func TestRunClassifiesNotFound(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run("show", Options{JSON: true, Stdout: &stdout, Stderr: &stderr, AuditPath: filepath.Join(dir, "a")},
		func(context.Context) (any, error) {
			return nil, resolve.NewNotFound("chat 7 missing")
		})
	if code != int(output.NotFound) {
		t.Fatalf("code = %d, want %d", code, output.NotFound)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"code":"NOT_FOUND"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunClassifiesAmbiguousIncludesCandidates(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	candidates := [][2]any{{int64(1), "Bjørn"}, {int64(2), "Bjarne"}}
	code := Run("show", Options{JSON: true, Stdout: &stdout, Stderr: &stderr, AuditPath: filepath.Join(dir, "a")},
		func(context.Context) (any, error) {
			return nil, &resolve.Ambiguous{Raw: "Bj", Candidates: candidates}
		})
	if code != int(output.BadArgs) {
		t.Fatalf("code = %d, want %d", code, output.BadArgs)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"candidates"`)) {
		t.Fatalf("stdout missing candidates: %q", stdout.String())
	}
}

func TestRunClassifiesLocalRateLimit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("send", Options{JSON: true, Stdout: &stdout, Stderr: &stderr},
		func(context.Context) (any, error) {
			return nil, &safety.LocalRateLimited{Msg: "slow down", RetryAfterSeconds: 7.5}
		})
	if code != int(output.LocalRateLimit) {
		t.Fatalf("code = %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"retry_after_seconds":7.5`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunClassifiesFloodWait(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("send", Options{JSON: true, Stdout: &stdout, Stderr: &stderr},
		func(context.Context) (any, error) {
			return nil, &safety.FloodWait{Seconds: 30}
		})
	if code != int(output.FloodWait) {
		t.Fatalf("code = %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"retry_after_seconds":30`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunClassifiesPremium(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("react", Options{JSON: true, Stdout: &stdout, Stderr: &stderr},
		func(context.Context) (any, error) {
			return nil, &safety.PremiumRequired{}
		})
	if code != int(output.PremiumRequired) {
		t.Fatalf("code = %d", code)
	}
}

func TestRunClassifiesPermissionDenied(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("send", Options{JSON: true, Stdout: &stdout, Stderr: &stderr}, func(context.Context) (any, error) {
		return nil, &safety.PermissionDenied{RPCType: "CHAT_WRITE_FORBIDDEN"}
	})
	if code != int(output.PermissionDenied) || !bytes.Contains(stdout.Bytes(), []byte(`"code":"PERMISSION_DENIED"`)) {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
}

func TestRunUnknownErrorMapsToGeneric(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("x", Options{JSON: true, Stdout: &stdout, Stderr: &stderr},
		func(context.Context) (any, error) {
			return nil, errors.New("boom")
		})
	if code != int(output.Generic) {
		t.Fatalf("code = %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"code":"GENERIC"`)) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunClassifiesCommittedCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	var stdout, stderr bytes.Buffer
	code := Run("backfill", Options{JSON: true, Stdout: &stdout, Stderr: &stderr, AuditPath: auditPath},
		func(context.Context) (any, error) {
			return nil, safety.NewCommittedWrite(
				"backfill rows committed but SQLite cleanup failed",
				safety.NewBadArgs("cleanup error with a classified cause"),
			)
		})
	if code != int(output.Generic) {
		t.Fatalf("code=%d, want GENERIC", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) || !bytes.Contains(stdout.Bytes(), []byte(`"partial":true`)) {
		t.Fatalf("stdout missing committed classification: %s", stdout.Bytes())
	}
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries=%d, want 1", len(entries))
	}
	entry := entries[0]
	if entry["result"] != "fail" || entry["error_code"] != "GENERIC" || entry["committed"] != true || entry["partial"] != true {
		t.Fatalf("audit missing durable committed classification: %#v", entry)
	}
}

func TestClassifyCommittedWriteMergesExtrasWithoutReservedOverrides(t *testing.T) {
	err := safety.NewCommittedWriteWithExtras("artifact committed", errors.New("finalization failed"), map[string]any{
		"artifact_path":  "/safe/media.bin",
		"artifact_bytes": int64(12),
		"committed":      false,
		"partial":        false,
		"cmd":            "forged",
		"request_id":     "forged",
		"error_code":     "BAD_ARGS",
	})
	code, _, extra := Classify(err)
	if code != output.Generic || extra["committed"] != true || extra["partial"] != true {
		t.Fatalf("classification=%v extras=%#v", code, extra)
	}
	if extra["artifact_path"] != "/safe/media.bin" || extra["artifact_bytes"] != int64(12) {
		t.Fatalf("artifact extras missing: %#v", extra)
	}
	for _, reserved := range []string{"cmd", "request_id", "error_code"} {
		if _, ok := extra[reserved]; ok {
			t.Fatalf("reserved extra %q survived: %#v", reserved, extra)
		}
	}
}

func TestRunDurableAuditFailureAfterSuccessReturnsCommittedFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := Run("download-media", Options{
		JSON: true, Stdout: &stdout, AuditPath: filepath.Join(blocker, "audit.log"), DurableAudit: true,
		CommittedExtras: map[string]any{"artifact_path": "/safe/media.bin", "artifact_bytes": int64(12)},
	}, func(context.Context) (any, error) { return map[string]any{"ok": true}, nil })
	if code != int(output.Generic) || !bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) || !bytes.Contains(stdout.Bytes(), []byte(`"artifact_path":"/safe/media.bin"`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunDurableAuditFailurePreservesPrecommitClassification(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := Run("download-media", Options{
		JSON: true, Stdout: &stdout, AuditPath: filepath.Join(blocker, "audit.log"), DurableAudit: true,
	}, func(context.Context) (any, error) { return nil, resolve.NewNotFound("message missing") })
	if code != int(output.NotFound) || !bytes.Contains(stdout.Bytes(), []byte(`"code":"NOT_FOUND"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"audit_failed":true`)) || bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunBlankDurableAuditPathAfterSuccessReturnsCommittedFailure(t *testing.T) {
	var stdout bytes.Buffer
	code := Run("download-media", Options{
		JSON: true, Stdout: &stdout, DurableAudit: true,
		CommittedExtras: map[string]any{"artifact_path": "/safe/media.bin", "artifact_bytes": int64(12)},
	}, func(context.Context) (any, error) { return map[string]any{"ok": true}, nil })
	if code != int(output.Generic) || !bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) || !bytes.Contains(stdout.Bytes(), []byte(`"artifact_path":"/safe/media.bin"`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunBlankDurableAuditPathPreservesPrecommitClassification(t *testing.T) {
	var stdout bytes.Buffer
	code := Run("download-media", Options{JSON: true, Stdout: &stdout, DurableAudit: true},
		func(context.Context) (any, error) { return nil, resolve.NewNotFound("message missing") })
	if code != int(output.NotFound) || !bytes.Contains(stdout.Bytes(), []byte(`"code":"NOT_FOUND"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"audit_failed":true`)) || bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunBlankDurableAuditPathPreservesCommittedClassification(t *testing.T) {
	var stdout bytes.Buffer
	code := Run("download-media", Options{JSON: true, Stdout: &stdout, DurableAudit: true},
		func(context.Context) (any, error) {
			return nil, safety.NewCommittedWriteWithExtras("download committed", errors.New("cache failed"), map[string]any{"artifact_bytes": int64(12)})
		})
	if code != int(output.Generic) || !bytes.Contains(stdout.Bytes(), []byte(`"committed":true`)) || !bytes.Contains(stdout.Bytes(), []byte(`"partial":true`)) || !bytes.Contains(stdout.Bytes(), []byte(`"artifact_bytes":12`)) || !bytes.Contains(stdout.Bytes(), []byte(`"audit_failed":true`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunNonDurableAuditFailureDoesNotChangeSuccess(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	code := Run("stats", Options{JSON: true, Stdout: &stdout, AuditPath: filepath.Join(blocker, "audit.log")},
		func(context.Context) (any, error) { return map[string]any{"ok": true}, nil })
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("code=%d output=%s", code, stdout.Bytes())
	}
}

func TestRunHumanFailureGoesToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("show", Options{JSON: false, Stdout: &stdout, Stderr: &stderr},
		func(context.Context) (any, error) {
			return nil, resolve.NewNotFound("missing")
		})
	if code != int(output.NotFound) {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("ERROR [NOT_FOUND]: missing")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
