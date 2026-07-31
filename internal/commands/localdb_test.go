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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func sqliteJournalMode(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return strings.ToLower(mode)
}

func TestDatabaseSizeBytesUsesSQLiteAllocatedPages(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	dbPath := cfg.Paths.(stubPaths).db
	db, err := store.Connect(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE wal_size_fixture(payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO wal_size_fixture(payload) VALUES (zeroblob(200000))"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(dbPath + "-wal")
	if err != nil {
		t.Fatalf("stat WAL: %v", err)
	}
	if walInfo.Size() == 0 {
		t.Fatal("WAL did not grow")
	}
	got, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if want := pageCount*pageSize + walInfo.Size(); got != want {
		t.Fatalf("size=%d, want logical main + WAL=%d", got, want)
	}
	if _, err := os.Stat(dbPath + "-shm"); err != nil {
		t.Fatalf("expected SHM coordination file: %v", err)
	}
}

func TestDatabaseSizeBytesReturnsPragmaErrors(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseSizeBytes(db); err == nil {
		t.Fatal("databaseSizeBytes on closed DB returned nil error")
	}
}

func TestBackfillRejectsReadOnlyBeforeClient(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	out, code := runRoot(t, cfg, "--read-only", "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 6 {
		t.Fatalf("code=%d, want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsWhenMaxAlreadyReachedBeforeRPC(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	text := "cached"
	for i := int64(1); i <= 2; i++ {
		if err := store.InsertMessage(db, store.Message{ChatID: 1, MessageID: i, Date: "2026-05-08T12:00:00", Text: &text}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 {
		t.Fatalf("client called despite cap: %#v", fc.Backfills)
	}
}

func TestBackfillRejectsDatabaseAlreadyOverCapBeforeClientOrMutation(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	paths := cfg.Paths.(stubPaths)
	db, err := store.Connect(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE cap_fixture(payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cap_fixture(payload) VALUES (zeroblob(1200000))"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := captureImmutableFile(t, paths.db)
	factoryCalls := 0
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		factoryCalls++
		return &client.FakeClient{}, nil
	}

	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 2 {
		t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls=%d, want 0", factoryCalls)
	}
	assertImmutableFile(t, paths.db, before)
	if _, err := os.Stat(paths.audit); !os.IsNotExist(err) {
		t.Fatalf("audit path was created: %v", err)
	}
	if _, err := os.Stat(paths.session); !os.IsNotExist(err) {
		t.Fatalf("session path was created: %v", err)
	}
}

func TestBackfillDatabaseCapStopsBeforeNextInsert(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, Date: "2026-05-08T12:00:00", Text: strings.Repeat("x", 1200000)},
		{ChatID: 1, MessageID: 11, Date: "2026-05-08T12:01:00", Text: "must be skipped"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"] != float64(0) || data["messages_skipped"] != float64(2) || data["db_cap_reached"] != true {
		t.Fatalf("data=%#v", data)
	}
	if data["db_size_bytes"].(float64) > 1024*1024 {
		t.Fatalf("db_size_bytes=%v exceeds cap", data["db_size_bytes"])
	}
	if warnings, ok := data["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("warnings=%#v, want non-null empty array", data["warnings"])
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id IN (10, 11)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("inserted fixture rows=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, cfg.Paths.(stubPaths).db); mode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", mode)
	}
}

func TestBackfillDatabaseCapRollsBackOnlyOversizedCandidate(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 20, Date: "2026-05-08T12:00:00", Text: strings.Repeat("a", 400000)},
		{ChatID: 1, MessageID: 21, Date: "2026-05-08T12:01:00", Text: strings.Repeat("b", 900000)},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--max-db-size-mb", "1", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"] != float64(1) || data["messages_skipped"] != float64(1) || data["db_cap_reached"] != true {
		t.Fatalf("data=%#v", data)
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if size > 1024*1024 {
		t.Fatalf("committed allocated size=%d exceeds cap", size)
	}
	var ids string
	if err := db.QueryRow("SELECT group_concat(message_id, ',') FROM tg_messages WHERE message_id IN (20,21)").Scan(&ids); err != nil {
		t.Fatal(err)
	}
	if ids != "20" {
		t.Fatalf("committed message ids=%q, want 20", ids)
	}
}

func TestBackfillInsertsMessagesAndWarnsNearCap(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.BackfillRows = []client.BackfillMessage{
		{ChatID: 1, MessageID: 10, SenderID: 99, Date: "2026-05-08T12:00:00", Text: "hi", IsOutgoing: true},
		{ChatID: 1, MessageID: 11, SenderID: 42, Date: "2026-05-08T12:01:00", Text: "there"},
	}
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "2", "--throttle-seconds", "1.5", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	data := env["data"].(map[string]any)
	if data["messages_inserted"].(float64) != 2 || data["messages_skipped"].(float64) != 0 || len(data["warnings"].([]any)) == 0 {
		t.Fatalf("data=%#v", data)
	}
	for _, key := range []string{"db_size_bytes", "db_cap_reached", "media_downloaded", "media_skipped", "media_failed", "warnings"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("data missing %q: %#v", key, data)
		}
	}
	if data["db_cap_reached"] != false || data["media_downloaded"] != float64(0) || data["media_skipped"] != float64(0) || data["media_failed"] != float64(0) {
		t.Fatalf("unexpected cap/media counters: %#v", data)
	}
	if len(fc.Backfills) != 1 || fc.Backfills[0].Throttle != 1500*time.Millisecond {
		t.Fatalf("backfills=%#v, want throttle 1.5s", fc.Backfills)
	}
	audit, err := os.ReadFile(cfg.Paths.(stubPaths).audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"max_db_size_mb":0`, `"throttle_seconds":1.5`, `"download_media":false`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %s: %s", want, audit)
		}
	}
}

func TestBackfillRejectsNegativeLimitsAndThrottle(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "messages", args: []string{"--max-messages", "-1"}},
		{name: "messages too large", args: []string{"--max-messages", "10001"}},
		{name: "database size", args: []string{"--max-db-size-mb", "-1"}},
		{name: "throttle", args: []string{"--throttle-seconds", "-0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fc, _ := setupWriteEnv(t)
			args := append([]string{"backfill", "1", "--allow-write", "--json"}, tc.args...)
			out, code := runRoot(t, cfg, args...)
			if code != 2 {
				t.Fatalf("code=%d, want BAD_ARGS=2\nout:%s", code, out)
			}
			if len(fc.Backfills) != 0 {
				t.Fatalf("client called: %#v", fc.Backfills)
			}
		})
	}
}

func TestBackfillThrottleDurationBoundaryDoesNotOverflow(t *testing.T) {
	maxSafeSeconds := math.Nextafter(float64(math.MaxInt64)/float64(time.Second), 0)
	d, err := backfillThrottleDuration(maxSafeSeconds)
	if err != nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	if d <= 0 {
		t.Fatalf("boundary duration=%v, want positive", d)
	}
	if _, err := backfillThrottleDuration(math.Nextafter(maxSafeSeconds, math.Inf(1))); err == nil {
		t.Fatal("overflowing next representable duration was accepted")
	} else {
		var badArgs *safety.BadArgs
		if !errors.As(err, &badArgs) {
			t.Fatalf("overflow error=%T, want *safety.BadArgs", err)
		}
	}
}

func TestBackfillPathLocksAreNormalizedAndPathScoped(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.sqlite")
	equivalentA := filepath.Join(dir, "nested", "..", "a.sqlite")
	pathB := filepath.Join(dir, "b.sqlite")

	unlockA, err := lockBackfillDBPath(pathA)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()

	bAcquired := make(chan func(), 1)
	go func() {
		unlock, lockErr := lockBackfillDBPath(pathB)
		if lockErr != nil {
			bAcquired <- nil
			return
		}
		bAcquired <- unlock
	}()
	select {
	case unlockB := <-bAcquired:
		if unlockB == nil {
			t.Fatal("locking unrelated DB failed")
		}
		unlockB()
	case <-time.After(time.Second):
		t.Fatal("unrelated database path was serialized")
	}

	sameAcquired := make(chan func(), 1)
	go func() {
		unlock, _ := lockBackfillDBPath(equivalentA)
		sameAcquired <- unlock
	}()
	select {
	case unlock := <-sameAcquired:
		if unlock != nil {
			unlock()
		}
		t.Fatal("equivalent database path was not serialized")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	unlockA = func() {}
	select {
	case unlock := <-sameAcquired:
		if unlock == nil {
			t.Fatal("equivalent database path lock failed")
		}
		unlock()
	case <-time.After(time.Second):
		t.Fatal("equivalent database path did not acquire after release")
	}
}

func TestCappedBackfillPrecommitFailureRollsBackAndRestoresWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantErr := errors.New("injected before commit")
	_, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 300, Date: "2026-08-01T12:00:00Z", Text: "not committed"}},
		backfillAtomicHooks{beforeCommit: func() error { return wantErr }},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want injected error", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=300").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("committed rows=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after precommit failure, want wal", mode)
	}
}

func TestCappedBackfillActiveReaderFailsBeforeWriteAndKeepsWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	readerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer readerDB.Close()
	reader, err := readerDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ExecContext(context.Background(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer reader.ExecContext(context.Background(), "ROLLBACK")
	var seeded int
	if err := reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM tg_chats").Scan(&seeded); err != nil {
		t.Fatal(err)
	}

	writerDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writerDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, _, err = insertBackfillRowsAtomic(ctx, writerDB, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 301, Date: "2026-08-01T12:00:00Z", Text: "blocked"}},
	)
	if err == nil {
		t.Fatal("capped write succeeded with an active reader")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("contention failure was not time-bounded: %v", time.Since(started))
	}
	var count int
	if err := reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM tg_messages WHERE message_id=301").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("blocked row count=%d, want 0", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after contention, want wal", mode)
	}
}

func TestCappedBackfillRacingReaderCannotInterruptWALRestore(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	committed := make(chan struct{})
	attempting := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		<-committed
		readerDB, openErr := sql.Open("sqlite", path)
		if openErr != nil {
			readerDone <- openErr
			return
		}
		defer readerDB.Close()
		readerDB.SetMaxOpenConns(1)
		if _, busyErr := readerDB.Exec("PRAGMA busy_timeout=1000"); busyErr != nil {
			readerDone <- busyErr
			return
		}
		close(attempting)
		var count int
		readerDone <- readerDB.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=302").Scan(&count)
	}()
	_, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 302, Date: "2026-08-01T12:00:00Z", Text: "committed"}},
		backfillAtomicHooks{afterCommit: func() error {
			close(committed)
			<-attempting
			select {
			case err := <-readerDone:
				return fmt.Errorf("reader entered before WAL restoration: %w", err)
			case <-time.After(50 * time.Millisecond):
				return nil
			}
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readerDone:
		if err != nil {
			t.Fatalf("reader failed after restoration: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("racing reader remained blocked after restoration")
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after success, want wal", mode)
	}
}

func TestCappedBackfillPostcommitFailureIsClassifiedAndRestoresWAL(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	wantErr := errors.New("injected after commit")
	_, _, _, err = insertBackfillRowsAtomicWithHooks(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: 303, Date: "2026-08-01T12:00:00Z", Text: "durable"}},
		backfillAtomicHooks{afterCommit: func() error { return wantErr }},
	)
	var committed *safety.CommittedWrite
	if !errors.As(err, &committed) || !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want classified committed error wrapping injected failure", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=303").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable row count=%d, want 1", count)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after postcommit error, want wal", mode)
	}
}

func TestBackfillCrossProcessHelper(t *testing.T) {
	if os.Getenv("TGCTL_BACKFILL_HELPER") != "1" {
		return
	}
	path := os.Getenv("TGCTL_BACKFILL_DB")
	id, err := strconv.ParseInt(os.Getenv("TGCTL_BACKFILL_ID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ready := os.Getenv("TGCTL_BACKFILL_READY")
	gate := os.Getenv("TGCTL_BACKFILL_GATE")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for subprocess gate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, _, _, err = insertBackfillRowsAtomic(context.Background(), db, 1, 10, 1024*1024,
		[]client.BackfillMessage{{ChatID: 1, MessageID: id, Date: "2026-08-01T12:00:00Z", Text: strings.Repeat("x", 650000)}},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCappedBackfillCrossProcessContentionRespectsCap(t *testing.T) {
	cfg, _, dir := setupWriteEnv(t)
	path := cfg.Paths.(stubPaths).db
	gate := filepath.Join(dir, "process-gate")
	type child struct {
		cmd *exec.Cmd
		out bytes.Buffer
	}
	children := make([]*child, 2)
	for i := range children {
		ready := filepath.Join(dir, fmt.Sprintf("ready-%d", i))
		child := &child{}
		child.cmd = exec.Command(os.Args[0], "-test.run=^TestBackfillCrossProcessHelper$")
		child.cmd.Env = append(os.Environ(),
			"TGCTL_BACKFILL_HELPER=1", "TGCTL_BACKFILL_DB="+path,
			"TGCTL_BACKFILL_ID="+strconv.Itoa(400+i), "TGCTL_BACKFILL_READY="+ready,
			"TGCTL_BACKFILL_GATE="+gate,
		)
		child.cmd.Stdout = &child.out
		child.cmd.Stderr = &child.out
		if err := child.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children[i] = child
	}
	deadline := time.Now().Add(5 * time.Second)
	for i := range children {
		ready := filepath.Join(dir, fmt.Sprintf("ready-%d", i))
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for child readiness")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, child := range children {
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("child failed: %v\n%s", err, child.out.String())
		}
	}
	db, err := store.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id IN (400,401)").Scan(&count); err != nil {
		t.Fatal(err)
	}
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || size > 1024*1024 {
		t.Fatalf("cross-process count=%d size=%d, want one row within cap", count, size)
	}
	if mode := sqliteJournalMode(t, path); mode != "wal" {
		t.Fatalf("journal_mode=%q after subprocess contention, want wal", mode)
	}
}

func setupLegacyBackfillEnv(t *testing.T) (CommandsConfig, *client.FakeClient, stubPaths) {
	t.Helper()
	dir := t.TempDir()
	paths := stubPaths{db: filepath.Join(dir, "telegram.sqlite"), session: filepath.Join(dir, "tg.session"), audit: filepath.Join(dir, "audit.log")}
	db, err := sql.Open("sqlite", paths.db)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := `
		CREATE TABLE tg_chats (chat_id INTEGER PRIMARY KEY, type TEXT, title TEXT, username TEXT);
		CREATE TABLE tg_messages (
			chat_id INTEGER, message_id INTEGER, sender_id INTEGER, date TEXT, text TEXT,
			is_outgoing INTEGER, reply_to_msg_id INTEGER, has_media INTEGER, media_type TEXT,
			raw_json TEXT, PRIMARY KEY(chat_id, message_id));
		CREATE INDEX idx_messages_chat_date ON tg_messages(chat_id, date DESC);
		CREATE INDEX idx_messages_date ON tg_messages(date DESC);
		INSERT INTO tg_chats(chat_id, type, title) VALUES (1, 'group', 'Legacy');`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fc := &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 1, MessageID: 7, Date: "2026-08-01T10:00:00Z", Text: "migrated"}}}
	cfg := CommandsConfig{
		Paths:                 paths,
		ClientFactory:         func(context.Context, string, string) (client.Client, error) { return fc, nil },
		ReadOnlyClientFactory: func(context.Context, string) (client.Client, error) { return fc, nil },
	}
	return cfg, fc, paths
}

func legacyHasColumn(t *testing.T, path, column string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA table_info(tg_messages)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	return false
}

func TestBackfillMigratesLegacySchemaBeforeCountAndInsert(t *testing.T) {
	cfg, fc, paths := setupLegacyBackfillEnv(t)
	out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Backfills) != 1 || !legacyHasColumn(t, paths.db, "deleted") || !legacyHasColumn(t, paths.db, "media_path") {
		t.Fatalf("migration/backfill missing: calls=%#v deleted=%v media_path=%v", fc.Backfills, legacyHasColumn(t, paths.db, "deleted"), legacyHasColumn(t, paths.db, "media_path"))
	}
	db, err := store.ConnectReadonly(paths.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE message_id=7 AND deleted=0").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("inserted rows=%d, want 1", count)
	}
}

func TestBackfillSafetyFailureDoesNotMigrateLegacySchema(t *testing.T) {
	cfg, fc, paths := setupLegacyBackfillEnv(t)
	before := captureImmutableFile(t, paths.db)
	out, code := runRoot(t, cfg, "--read-only", "backfill", "1", "--max-messages", "10", "--allow-write", "--json")
	if code != 6 {
		t.Fatalf("code=%d, want WRITE_DISALLOWED=6\nout:%s", code, out)
	}
	if len(fc.Backfills) != 0 || legacyHasColumn(t, paths.db, "deleted") {
		t.Fatalf("safety failure mutated/called client: calls=%#v", fc.Backfills)
	}
	assertImmutableFile(t, paths.db, before)
}

type concurrentBackfillResult struct {
	code int
	out  string
}

func runConcurrentBackfills(t *testing.T, cfg CommandsConfig, rowText string, maxMessages, maxDBSizeMB int) []concurrentBackfillResult {
	t.Helper()
	var calls int32
	ready := make(chan struct{})
	cfg.ClientFactory = func(context.Context, string, string) (client.Client, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 2 {
			close(ready)
		}
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
			return nil, errors.New("timed out waiting for concurrent backfill client")
		}
		return &client.FakeClient{BackfillRows: []client.BackfillMessage{{ChatID: 1, MessageID: int64(100 + n), Date: "2026-08-01T10:00:00Z", Text: rowText}}}, nil
	}
	results := make(chan concurrentBackfillResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			out, code := runRoot(t, cfg, "backfill", "1", "--max-messages", fmt.Sprint(maxMessages), "--max-db-size-mb", fmt.Sprint(maxDBSizeMB), "--allow-write", "--json")
			results <- concurrentBackfillResult{code: code, out: out}
		}()
	}
	return []concurrentBackfillResult{<-results, <-results}
}

func TestConcurrentBackfillsAtomicallyEnforceMessageLimit(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	for _, result := range runConcurrentBackfills(t, cfg, "small", 1, 0) {
		if result.code != 0 {
			t.Fatalf("code=%d, want 0\nout:%s", result.code, result.out)
		}
	}
	db, err := store.ConnectReadonly(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message count=%d exceeds max 1", count)
	}
}

func TestConcurrentBackfillsAtomicallyEnforceDatabaseCap(t *testing.T) {
	cfg, _, _ := setupWriteEnv(t)
	for _, result := range runConcurrentBackfills(t, cfg, strings.Repeat("x", 650000), 10, 1) {
		if result.code != 0 {
			t.Fatalf("code=%d, want 0\nout:%s", result.code, result.out)
		}
	}
	db, err := store.Connect(cfg.Paths.(stubPaths).db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	size, err := databaseSizeBytes(db)
	if err != nil {
		t.Fatal(err)
	}
	if size > 1024*1024 {
		t.Fatalf("allocated size=%d exceeds cap", size)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tg_messages WHERE chat_id=1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message count=%d, want exactly one fitting row", count)
	}
}

func TestDiscoverUpsertsChats(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Dialogs = []client.ChatInfo{{ID: 2, Type: "channel", Title: "Ops", Username: "ops"}}
	out, code := runRoot(t, cfg, "discover", "--limit", "10", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.Discoveries) != 1 {
		t.Fatalf("discoveries=%#v", fc.Discoveries)
	}
	db, _ := store.Connect(cfg.Paths.(stubPaths).db)
	defer db.Close()
	var title string
	if err := db.QueryRow("SELECT title FROM tg_chats WHERE chat_id=2").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Ops" {
		t.Fatalf("title=%q", title)
	}
}

func TestSyncContactsUpsertsContacts(t *testing.T) {
	cfg, fc, _ := setupWriteEnv(t)
	fc.Contacts = []client.ContactInfo{{UserID: 42, Phone: "123", FirstName: "Ada", Username: "ada", IsMutual: true}}
	out, code := runRoot(t, cfg, "sync-contacts", "--allow-write", "--json")
	if code != 0 {
		t.Fatalf("code=%d\nout:%s", code, out)
	}
	if len(fc.ContactSyncs) != 1 {
		t.Fatalf("sync calls=%#v", fc.ContactSyncs)
	}
	if !strings.Contains(out, `"synced":1`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
