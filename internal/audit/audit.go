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
		"args":       args,
		"result":     result,
	}
	for k, v := range extra {
		entry[k] = v
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
		"ts":                  time.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		"phase":               "before",
		"cmd":                 e.Cmd,
		"request_id":          e.RequestID,
		"resolved_chat_id":    e.ResolvedChatID,
		"resolved_chat_title": e.ResolvedChatTitle,
		"telethon_method":     e.TelethonMethod,
		"payload_preview":     e.PayloadPreview,
		"dry_run":             e.DryRun,
	}
	return appendEntry(path, entry)
}

func appendEntry(path string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, string(encoded)); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}
