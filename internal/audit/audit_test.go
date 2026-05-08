package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", scanner.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func TestWriteAppendsLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	if err := Write(path, "stats", "req-1", map[string]any{"a": 1}, "ok", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Write(path, "show", "req-2", nil, "fail", map[string]any{"error_code": "NOT_FOUND"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries := readJSONLines(t, path)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0]["cmd"] != "stats" || entries[0]["request_id"] != "req-1" || entries[0]["result"] != "ok" {
		t.Fatalf("first = %#v", entries[0])
	}
	if entries[1]["error_code"] != "NOT_FOUND" {
		t.Fatalf("second extra missing: %#v", entries[1])
	}
}

func TestWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "audit.log")
	if err := Write(path, "x", "r", nil, "ok", nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

func TestPreIncludesAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	err := Pre(path, PreEntry{
		Cmd:               "send",
		RequestID:         "req-9",
		ResolvedChatID:    -100123,
		ResolvedChatTitle: "Bjørn",
		TelethonMethod:    "messages.SendMessage",
		PayloadPreview:    map[string]any{"text": "hi"},
		DryRun:            true,
	})
	if err != nil {
		t.Fatalf("Pre: %v", err)
	}
	entries := readJSONLines(t, path)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry")
	}
	e := entries[0]
	if e["phase"] != "before" || e["cmd"] != "send" || e["telethon_method"] != "messages.SendMessage" {
		t.Fatalf("entry = %#v", e)
	}
	if e["dry_run"] != true {
		t.Fatalf("dry_run = %#v", e["dry_run"])
	}
	if e["resolved_chat_title"] != "Bjørn" {
		t.Fatalf("title = %#v", e["resolved_chat_title"])
	}
}
