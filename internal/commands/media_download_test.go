package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/media"
	"github.com/b1rd33/tgctl-go/internal/store"
)

type downloadGatePaths struct {
	root         string
	currentCalls int
	pathCalls    int
}

func (p *downloadGatePaths) Current() string {
	p.currentCalls++
	return "default"
}

func (p *downloadGatePaths) AccountPaths(string) (string, string, string, error) {
	p.pathCalls++
	dir := filepath.Join(p.root, "accounts", "default")
	if err := os.MkdirAll(filepath.Join(dir, "media"), 0o700); err != nil {
		return "", "", "", err
	}
	return filepath.Join(dir, "telegram.sqlite"), filepath.Join(dir, "tg.session"), filepath.Join(dir, "audit.log"), nil
}

func TestDownloadMediaCommandHelpContract(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	root := NewRootCommand()
	registerMediaCommands(root, cfg)
	cmd, _, err := root.Find([]string{"download-media"})
	if err != nil {
		t.Fatalf("find command: %v", err)
	}
	if cmd.Use != "download-media <chat> <message-id>" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]string{
		"allow-write": "false", "fuzzy": "false", "json": "false", "human": "false",
		"max-size-mb": "100", "output": "", "overwrite": "false",
	}
	for name, def := range want {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("missing --%s", name)
		}
		if flag.DefValue != def {
			t.Fatalf("--%s default = %q, want %q", name, flag.DefValue, def)
		}
	}
	for _, unexpected := range []string{"confirm", "dry-run", "idempotency-key"} {
		if cmd.Flags().Lookup(unexpected) != nil {
			t.Fatalf("unexpected destructive flag --%s", unexpected)
		}
	}
}

func TestDownloadMediaWriteGatePrecedesEveryMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing allow", args: []string{"download-media", "1", "9", "--json"}},
		{name: "read only wins", args: []string{"--read-only", "download-media", "1", "9", "--allow-write", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootDir := t.TempDir()
			paths := &downloadGatePaths{root: rootDir}
			factoryCalls := 0
			cfg := CommandsConfig{Paths: paths, ClientFactory: func(context.Context, string, string) (client.Client, error) {
				factoryCalls++
				return &client.FakeClient{}, nil
			}}
			out, code := runRoot(t, cfg, tc.args...)
			if code != 6 {
				t.Fatalf("code=%d want WRITE_DISALLOWED=6\nout:%s", code, out)
			}
			if paths.currentCalls != 0 || paths.pathCalls != 0 || factoryCalls != 0 {
				t.Fatalf("calls current/path/client = %d/%d/%d", paths.currentCalls, paths.pathCalls, factoryCalls)
			}
			entries, err := os.ReadDir(rootDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("gate created filesystem entries: %v", entries)
			}
		})
	}
}

func TestDownloadMediaRejectsInvalidMessageAndSizeBeforePaths(t *testing.T) {
	tooManyMB := int64(math.MaxInt64/(1024*1024) + 1)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "zero message", args: []string{"download-media", "1", "0", "--allow-write", "--json"}},
		{name: "negative message", args: []string{"download-media", "--allow-write", "--json", "--", "1", "-1"}},
		{name: "non integer message", args: []string{"download-media", "1", "nine", "--allow-write", "--json"}},
		{name: "message exceeds int32", args: []string{"download-media", "1", fmt.Sprint(int64(math.MaxInt32) + 1), "--allow-write", "--json"}},
		{name: "negative size", args: []string{"download-media", "1", "9", "--max-size-mb", "-1", "--allow-write", "--json"}},
		{name: "size multiplication overflow", args: []string{"download-media", "1", fmt.Sprint(tooManyMB), "--max-size-mb", fmt.Sprint(tooManyMB), "--allow-write", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := &downloadGatePaths{root: t.TempDir()}
			cfg := CommandsConfig{Paths: paths, ClientFactory: func(context.Context, string, string) (client.Client, error) {
				t.Fatal("client called")
				return nil, nil
			}}
			out, code := runRoot(t, cfg, tc.args...)
			if code != 2 {
				t.Fatalf("code=%d want BAD_ARGS=2\nout:%s", code, out)
			}
			if paths.currentCalls != 0 || paths.pathCalls != 0 {
				t.Fatalf("paths touched current/path=%d/%d", paths.currentCalls, paths.pathCalls)
			}
		})
	}
}

func configureDownload(t *testing.T, cfg CommandsConfig, fake *client.FakeClient, chatID, messageID int64, outputDir string, skipped bool) string {
	t.Helper()
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(absOutput, "asset.bin")
	if err := os.MkdirAll(absOutput, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("hello world!"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, _, err := media.CaptureArtifactIdentity(absOutput, mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	fake.DownloadResp = client.DownloadMediaResp{
		ChatID: chatID, MessageID: messageID, MediaType: "document", MIMEType: "application/octet-stream",
		Filename: "asset.bin", Path: mediaPath, Bytes: 12, Skipped: skipped,
		MessageDate:      time.Date(2026, 7, 31, 15, 4, 5, 0, time.UTC),
		ArtifactIdentity: identity,
	}
	return mediaPath
}

func TestDownloadMediaNumericSelectorDefaultDirectoryAndEnvelope(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	defaultDir := filepath.Join(dir, "media", "1")
	wantPath := configureDownload(t, cfg, fake, 1, 9, defaultDir, false)

	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fake.Downloads) != 1 {
		t.Fatalf("downloads=%#v", fake.Downloads)
	}
	req := fake.Downloads[0]
	if req.ChatID != 1 || req.MessageID != 9 || req.OutputDir != defaultDir || req.MaxBytes != 100*1024*1024 || req.Overwrite {
		t.Fatalf("request=%#v", req)
	}
	var env struct {
		OK        bool                     `json:"ok"`
		Command   string                   `json:"command"`
		RequestID string                   `json:"request_id"`
		Data      client.DownloadMediaResp `json:"data"`
		Warnings  []string                 `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Command != "download-media" || !strings.HasPrefix(env.RequestID, "req-") || env.Data.Path != wantPath || env.Warnings == nil || len(env.Warnings) != 0 {
		t.Fatalf("envelope=%#v", env)
	}
	msg, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9)
	if err != nil {
		t.Fatal(err)
	}
	if msg.MediaPath == nil || *msg.MediaPath != wantPath || msg.MediaType == nil || *msg.MediaType != "document" {
		t.Fatalf("cached message=%#v", msg)
	}
	assertAuditContains(t, filepath.Join(dir, "audit.log"),
		`"chat":"1"`, `"message_id":9`, `"max_size_mb":100`, `"overwrite":false`, `"output_policy":"default"`,
		`"artifact_path":"`+strings.ReplaceAll(wantPath, `\`, `\\`)+`"`, `"artifact_bytes":12`,
		`"media_type":"document"`, `"mime_type":"application/octet-stream"`, `"filename":"asset.bin"`, `"skipped":false`)
}

func TestDownloadMediaFuzzySelectorRequiresFlagAndResolvesOnce(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "download-media", "Bjørn", "9", "--allow-write", "--json")
	if code != 2 || len(fake.Downloads) != 0 {
		t.Fatalf("without fuzzy code=%d downloads=%#v out=%s", code, fake.Downloads, out)
	}
	configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), false)
	out, code = runRoot(t, cfg, "download-media", "Bjørn", "9", "--fuzzy", "--allow-write", "--json")
	if code != 0 || len(fake.Downloads) != 1 || fake.Downloads[0].ChatID != 1 {
		t.Fatalf("with fuzzy code=%d downloads=%#v out=%s", code, fake.Downloads, out)
	}
}

func TestDownloadMediaUnknownSelectorDoesNotCallClient(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "download-media", "404", "9", "--allow-write", "--json")
	if code != 4 || len(fake.Downloads) != 0 {
		t.Fatalf("code=%d downloads=%#v out=%s", code, fake.Downloads, out)
	}
}

func TestDownloadMediaExplicitRelativeOutputAndUnlimitedSize(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	absoluteOutput := filepath.Join(t.TempDir(), "chosen")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(workingDir, absoluteOutput)
	if err != nil {
		t.Fatal(err)
	}
	configureDownload(t, cfg, fake, 1, 9, relative, false)
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--output", relative, "--max-size-mb", "0", "--overwrite", "--allow-write", "--human")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fake.Downloads) != 1 || fake.Downloads[0].OutputDir != relative || fake.Downloads[0].MaxBytes != 0 || !fake.Downloads[0].Overwrite {
		t.Fatalf("request=%#v", fake.Downloads)
	}
	if !strings.Contains(out, `"media_path"`) || !strings.Contains(out, `"skipped": false`) {
		t.Fatalf("human output=%s", out)
	}
}

func TestDownloadMediaUpdatesExistingAndSkippedCacheRows(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("skipped_%v", skipped), func(t *testing.T) {
			cfg, fake, dir := setupWriteEnv(t)
			db, err := store.Connect(cfg.Paths.(stubPaths).db)
			if err != nil {
				t.Fatal(err)
			}
			oldType, oldPath := "photo", "/tmp/old.jpg"
			if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: 9, Date: "2026-01-01T00:00:00Z", HasMedia: true, MediaType: &oldType, MediaPath: &oldPath}); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			wantPath := configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), skipped)
			if out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json"); code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			got, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9)
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaPath == nil || *got.MediaPath != wantPath {
				t.Fatalf("message=%#v", got)
			}
		})
	}
}

func TestDownloadMediaRejectsFaultyResponsesWithoutCacheMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*client.DownloadMediaResp, string)
	}{
		{name: "mismatched chat", edit: func(r *client.DownloadMediaResp, _ string) { r.ChatID = 2 }},
		{name: "mismatched message", edit: func(r *client.DownloadMediaResp, _ string) { r.MessageID = 10 }},
		{name: "blank media type", edit: func(r *client.DownloadMediaResp, _ string) { r.MediaType = " " }},
		{name: "unknown media type", edit: func(r *client.DownloadMediaResp, _ string) { r.MediaType = "executable" }},
		{name: "blank mime", edit: func(r *client.DownloadMediaResp, _ string) { r.MIMEType = " " }},
		{name: "malformed mime", edit: func(r *client.DownloadMediaResp, _ string) { r.MIMEType = "not a mime" }},
		{name: "blank filename", edit: func(r *client.DownloadMediaResp, _ string) { r.Filename = " " }},
		{name: "filename mismatch", edit: func(r *client.DownloadMediaResp, _ string) { r.Filename = "other.bin" }},
		{name: "unsanitized filename", edit: func(r *client.DownloadMediaResp, output string) {
			r.Filename = "bad?.bin"
			r.Path = filepath.Join(output, r.Filename)
			_ = os.WriteFile(r.Path, bytes.Repeat([]byte{'x'}, int(r.Bytes)), 0o600)
		}},
		{name: "negative bytes", edit: func(r *client.DownloadMediaResp, _ string) { r.Bytes = -1 }},
		{name: "missing artifact identity", edit: func(r *client.DownloadMediaResp, _ string) {
			r.ArtifactIdentity = media.ArtifactIdentity{}
		}},
		{name: "size mismatch", edit: func(r *client.DownloadMediaResp, _ string) { r.Bytes++ }},
		{name: "relative path", edit: func(r *client.DownloadMediaResp, _ string) { r.Path = "asset.bin" }},
		{name: "outside output", edit: func(r *client.DownloadMediaResp, output string) {
			r.Path = filepath.Join(filepath.Dir(output), "escape.bin")
		}},
		{name: "missing entry", edit: func(r *client.DownloadMediaResp, _ string) { _ = os.Remove(r.Path) }},
		{name: "directory entry", edit: func(r *client.DownloadMediaResp, _ string) {
			_ = os.Remove(r.Path)
			_ = os.Mkdir(r.Path, 0o700)
		}},
		{name: "symlink entry", edit: func(r *client.DownloadMediaResp, output string) {
			_ = os.Remove(r.Path)
			victim := filepath.Join(filepath.Dir(output), "victim.bin")
			_ = os.WriteFile(victim, []byte("hello world!"), 0o600)
			_ = os.Symlink(victim, r.Path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fake, dir := setupWriteEnv(t)
			output := filepath.Join(dir, "media", "1")
			configureDownload(t, cfg, fake, 1, 9, output, false)
			tc.edit(&fake.DownloadResp, output)
			out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
			if code != 1 {
				t.Fatalf("code=%d want GENERIC=1 out=%s", code, out)
			}
			if !strings.Contains(out, `"committed":true`) || strings.Contains(out, fake.DownloadResp.Path) {
				t.Fatalf("invalid non-skipped response lacks conservative classification or leaked path: %s", out)
			}
			if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("cache mutated, err=%v", err)
			}
		})
	}
}

func TestDownloadMediaRejectsInvalidSkippedResponseAsCommitted(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "media", "1")
	configureDownload(t, cfg, fake, 1, 9, output, true)
	fake.DownloadResp.MIMEType = "not a mime"
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"partial":true`) || strings.Contains(out, fake.DownloadResp.Path) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache mutated: %v", err)
	}
}

func TestDownloadMediaRejectsSkippedOverwriteAsIncoherent(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), true)
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--overwrite", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"partial":true`) || strings.Contains(out, fake.DownloadResp.Path) {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestDownloadMediaRejectsOutputRootReplacementAfterClientReturn(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "media", "1")
	path := configureDownload(t, cfg, fake, 1, 9, output, false)
	fake.DownloadHook = func() {
		if err := os.Rename(output, output+"-original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(output, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("evil payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || strings.Contains(out, path) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache mutated: %v", err)
	}
}

func TestDownloadMediaRejectsSameSizeFileReplacementAfterClientReturn(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	output := filepath.Join(dir, "media", "1")
	path := configureDownload(t, cfg, fake, 1, 9, output, false)
	fake.DownloadHook = func() {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("evil payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || strings.Contains(out, path) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache mutated: %v", err)
	}
}

func TestDownloadMediaClientFailureDoesNotUpdateCache(t *testing.T) {
	cfg, fake, _ := setupWriteEnv(t)
	fake.DownloadErr = errors.New("download failed")
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 1 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cache mutated, err=%v", err)
	}
}

func TestDownloadMediaPersistenceFailureMarksCommittedOnlyForNewDownload(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("skipped_%v", skipped), func(t *testing.T) {
			cfg, fake, dir := setupWriteEnv(t)
			db, err := store.Connect(cfg.Paths.(stubPaths).db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TRIGGER reject_download_cache BEFORE INSERT ON tg_messages BEGIN SELECT RAISE(ABORT, 'cache unavailable'); END`); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), skipped)
			out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
			if code != 1 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			if got := strings.Contains(out, `"committed":true`); got != !skipped {
				t.Fatalf("committed metadata=%v want %v out=%s", got, !skipped, out)
			}
			auditPath := filepath.Join(dir, "audit.log")
			if !skipped {
				for _, fragment := range []string{
					`"committed":true`, `"partial":true`,
					`"artifact_path":"` + strings.ReplaceAll(fake.DownloadResp.Path, `\`, `\\`) + `"`,
					`"artifact_bytes":12`, `"media_type":"document"`, `"mime_type":"application/octet-stream"`,
					`"filename":"asset.bin"`, `"skipped":false`,
				} {
					if !strings.Contains(out, fragment) {
						t.Fatalf("output missing %s: %s", fragment, out)
					}
				}
				assertAuditContains(t, auditPath,
					`"committed":true`, `"partial":true`, `"error_code":"GENERIC"`,
					`"artifact_path":"`+strings.ReplaceAll(fake.DownloadResp.Path, `\`, `\\`)+`"`,
					`"artifact_bytes":12`, `"media_type":"document"`, `"mime_type":"application/octet-stream"`,
					`"filename":"asset.bin"`, `"skipped":false`)
			} else {
				assertAuditContains(t, auditPath, `"artifact_bytes":12`, `"skipped":true`)
			}
		})
	}
}

func TestDownloadMediaClientCloseFailureUsesValidatedRecoveryMetadata(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(fmt.Sprintf("skipped_%v", skipped), func(t *testing.T) {
			cfg, fake, dir := setupWriteEnv(t)
			configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), skipped)
			fake.CloseErr = errors.New("client close failed")
			out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
			if code != 1 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			if got := strings.Contains(out, `"committed":true`); got != !skipped {
				t.Fatalf("committed=%v want %v out=%s", got, !skipped, out)
			}
			if !skipped && (!strings.Contains(out, `"artifact_bytes":12`) || !strings.Contains(out, `"filename":"asset.bin"`)) {
				t.Fatalf("recovery metadata missing: %s", out)
			}
			if _, err := loadMessage(cfg.Paths.(stubPaths).db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("cache persisted despite client finalization failure: %v", err)
			}
		})
	}
}

func TestDownloadMediaDurableAuditFailureAfterSuccessIsCommitted(t *testing.T) {
	cfg, fake, dir := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	blocker := filepath.Join(dir, "audit-blocker")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths.audit = filepath.Join(blocker, "audit.log")
	cfg.Paths = paths
	configureDownload(t, cfg, fake, 1, 9, filepath.Join(dir, "media", "1"), false)
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 1 || !strings.Contains(out, `"committed":true`) || !strings.Contains(out, `"artifact_bytes":12`) || !strings.Contains(out, `"filename":"asset.bin"`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if _, err := loadMessage(paths.db, 1, 9); err != nil {
		t.Fatalf("cache was not committed before audit failure: %v", err)
	}
}

func TestDownloadMediaDurableAuditFailurePreservesPrecommitError(t *testing.T) {
	cfg, _, dir := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	blocker := filepath.Join(dir, "audit-blocker")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths.audit = filepath.Join(blocker, "audit.log")
	cfg.Paths = paths
	out, code := runRoot(t, cfg, "download-media", "404", "9", "--allow-write", "--json")
	if code != 4 || !strings.Contains(out, `"code":"NOT_FOUND"`) || !strings.Contains(out, `"audit_failed":true`) || strings.Contains(out, `"committed":true`) {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestDownloadMediaUsesOneAccountSnapshotForDBSessionAuditAndDefaultMedia(t *testing.T) {
	rootDir := t.TempDir()
	first := stubPaths{db: filepath.Join(rootDir, "first", "telegram.sqlite"), session: filepath.Join(rootDir, "first", "tg.session"), audit: filepath.Join(rootDir, "first", "audit.log")}
	second := stubPaths{db: filepath.Join(rootDir, "second", "telegram.sqlite"), session: filepath.Join(rootDir, "second", "tg.session"), audit: filepath.Join(rootDir, "second", "audit.log")}
	seedChat(t, first.db, 1, "First")
	seedChat(t, second.db, 2, "Second")
	paths := &changingAccountPaths{firstName: "first", secondName: "second", first: first, second: second}
	fake := &client.FakeClient{}
	wantOutput := filepath.Join(filepath.Dir(first.db), "media", "1")
	configureDownload(t, CommandsConfig{}, fake, 1, 9, wantOutput, false)
	var gotSession, gotDB string
	cfg := CommandsConfig{Paths: paths, ClientFactory: func(_ context.Context, sessionPath, dbPath string) (client.Client, error) {
		gotSession, gotDB = sessionPath, dbPath
		return fake, nil
	}}
	out, code := runRoot(t, cfg, "download-media", "1", "9", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if paths.calls != 1 || gotSession != first.session || gotDB != first.db || fake.Downloads[0].OutputDir != wantOutput {
		t.Fatalf("snapshot calls=%d session=%q db=%q request=%#v", paths.calls, gotSession, gotDB, fake.Downloads)
	}
	if _, err := loadMessage(first.db, 1, 9); err != nil {
		t.Fatalf("first cache: %v", err)
	}
	if _, err := loadMessage(second.db, 1, 9); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second cache changed: %v", err)
	}
	assertAuditContains(t, first.audit, `"cmd":"download-media"`)
	assertPathMissing(t, second.audit)
}

func loadMessage(dbPath string, chatID, messageID int64) (*store.Message, error) {
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return store.GetOne(db, chatID, messageID, true)
}
