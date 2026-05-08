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
