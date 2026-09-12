package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Write appends one JSONL audit entry. Creates parent dirs as needed and
// chmods the file to 0600 on Unix-like systems where supported.
func Write(path string, cmd, requestID string, args map[string]any, result string, extra map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	entry := map[string]any{
		"ts":         time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		"cmd":        cmd,
		"request_id": requestID,
		"args":       safeArgs(args),
		"result":     result,
	}
	for k, v := range extra {
		switch k {
		case "error_code", "committed", "partial", "audit_failed", "retry_after_seconds", "telegram_error", "artifact_bytes", "media_type", "mime_type", "skipped":
			if primitiveAuditValue(v) {
				entry[k] = v
			}
		}
	}
	return appendEntry(path, entry)
}

// PreEntry are the fields written by Pre. Mirrors tgcli.safety.audit_pre.
type PreEntry struct {
	Cmd               string
	RequestID         string
	ResolvedChatID    int64
	ResolvedChatTitle string
	TelethonMethod    string
	PayloadPreview    map[string]any
	DryRun            bool
}

// Pre appends the pre-call write audit entry.
func Pre(path string, e PreEntry) error {
	if e.PayloadPreview == nil {
		e.PayloadPreview = map[string]any{}
	}
	entry := map[string]any{
		"ts":         time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		"phase":      "before",
		"cmd":        e.Cmd,
		"request_id": e.RequestID,

		"telethon_method": e.TelethonMethod,

		"dry_run": e.DryRun,
	}
	return appendEntry(path, entry)
}

func appendEntry(path string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("audit path must be a regular file without symlinks")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, string(encoded)); err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	return f.Sync()
}

func safeArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"max_db_size_mb", "throttle_seconds", "download_media", "max_media_size_mb", "overwrite_media", "media_dir_policy", "max_size_mb", "overwrite", "output_policy", "dry_run", "limit", "artifact_bytes", "media_type", "mime_type", "skipped"} {
		if v, ok := args[key]; ok {
			if primitiveAuditValue(v) {
				out[key] = v
			}
		}
	}
	return out
}

func primitiveAuditValue(v any) bool {
	switch v.(type) {
	case string, bool, int, int32, int64, uint, uint32, uint64, float32, float64, json.Number:
		return true
	}
	return false
}
